# syntax=docker/dockerfile:1

## Multistage build: First stage fetches helper scripts
FROM alpine:3.23 AS fetcher

# download JSON.sh
RUN apk update && \
    apk --no-cache add ca-certificates && \
    wget -O /JSON.sh https://raw.githubusercontent.com/dominictarr/JSON.sh/0d5e5c77365f63809bf6e77ef44a1f34b0e05840/JSON.sh

# Fetch the official, self-updatable yt-dlp release. Remote ADD participates in
# Docker's cache validation, unlike a cached RUN command that downloads "latest".
ADD https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp /yt-dlp
ADD https://github.com/yt-dlp/yt-dlp/releases/latest/download/SHA2-256SUMS /SHA2-256SUMS
RUN grep '  yt-dlp$' /SHA2-256SUMS | (cd / && sha256sum -c -) && \
    chmod 0755 /yt-dlp

## Second stage: Use Alpine for the final runtime environment
FROM alpine:3.23

# Install runtime dependencies. ffmpeg includes ffprobe for video thumbnails.
# Alpine's yt-dlp package supplies Python, EJS, and JavaScript runtime support;
# the current official executable copied below supplies the yt-dlp application.
RUN apk --no-cache add ca-certificates ffmpeg mailcap tini-static yt-dlp

# Define non-root user UID and GID
ENV UID=1000
ENV GID=1000
ENV PATH="/opt/filebrowser/bin:${PATH}"

# Create user group and user
RUN addgroup -g $GID user && \
    adduser -D -u $UID -G user user

# Copy binary, scripts, and configurations into image with proper ownership
COPY --chown=user:user filebrowser /bin/filebrowser
COPY --chown=user:user docker/common/ /
COPY --chown=user:user docker/alpine/ /
COPY --from=fetcher /JSON.sh /JSON.sh
COPY --from=fetcher --chown=user:user --chmod=0755 /yt-dlp /opt/filebrowser/bin/yt-dlp

# Create data directories, set ownership, and ensure healthcheck script is executable
RUN mkdir -p /config /database /srv && \
    chown -R user:user /opt/filebrowser/bin /config /database /srv \
    && chmod +x /healthcheck.sh

# Define healthcheck script
HEALTHCHECK --start-period=2s --interval=5s --timeout=3s CMD /healthcheck.sh

# Set the user, volumes and exposed ports
USER user

VOLUME /srv /config /database

EXPOSE 80

ENTRYPOINT [ "/sbin/tini-static", "--", "/init.sh" ]
