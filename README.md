> [!WARNING]
>
> The original `filebrowser/filebrowser` project plans to archive its repository
> on 2026-09-01 and has shipped its final planned release. This fork remains an
> independent project and includes upstream's final security and maintenance fixes.

<p align="center">
  <img src="./branding/banner.png" width="550" alt="File Browser"/>
</p>

<p align="center">
  <strong>A modern, actively developed fork of File Browser for self-hosted file management.</strong>
</p>

<p align="center">
  <a href="https://github.com/aeltawela/filebrowser/actions/workflows/ci.yaml">
    <img alt="Build" src="https://github.com/aeltawela/filebrowser/actions/workflows/ci.yaml/badge.svg?branch=master">
  </a>
  <a href="https://goreportcard.com/report/github.com/filebrowser/filebrowser/v2">
    <img alt="Go Report Card" src="https://goreportcard.com/badge/github.com/filebrowser/filebrowser/v2">
  </a>
  <a href="LICENSE">
    <img alt="License" src="https://img.shields.io/badge/license-Apache--2.0-blue">
  </a>
  <img alt="Fork status" src="https://img.shields.io/badge/fork-active%20feature%20development-2ea44f">
</p>

File Browser gives you a clean web interface for a directory you control. Upload,
download, rename, move, delete, preview, and edit files from a browser while
keeping deployment simple: one Go backend, a Vue frontend, and a self-hosted
runtime that fits small servers as well as larger installations.

This fork keeps that spirit and pushes it forward with practical improvements
for day-to-day media, download, and navigation workflows.

## Why This Fork Exists

The upstream `filebrowser/filebrowser` project has shipped its final planned
release and plans to archive its repository on 2026-09-01. It remains an
important project, but it will no longer receive bug or security fixes.

This fork exists to keep File Browser useful for people who still want active
feature development:

- Ship focused quality-of-life improvements without waiting on upstream feature
  review.
- Preserve the familiar File Browser experience and configuration model.
- Keep optional runtime features graceful: missing tools should fall back instead
  of breaking browsing.
- Continue syncing upstream maintenance work where it makes sense.

## What This Fork Adds

| Area | Delta in this fork |
| --- | --- |
| Navigation | File and folder bookmarks, available from the sidebar and file views, so frequently used paths stay one click away. |
| Link downloads | Paste HTTP(S) links and save them directly into the current user scope, with job status, cancellation, and direct-download fallback. |
| Media downloads | Optional `yt-dlp` integration for media-site links, quality selection, audio-only downloads, custom format selectors, and an admin-triggered updater. |
| Video previews | Cached video thumbnails generated through `ffmpeg` and `ffprobe`, with Docker images including the required tools. |
| Runtime controls | Tunable video thumbnail worker count and timeout through CLI/config settings for low-resource and higher-throughput deployments. |
| HTML preview | Opt-in full HTML file previews served through a sandboxed preview endpoint. |
| Performance | Reduced unnecessary file metadata and preview work in hot browsing paths. |
| Tooling | Refreshed Go and frontend dependencies, with the frontend on Vue, Vite, TypeScript, and pnpm. |

## Core Features

- Browse, upload, download, move, copy, rename, delete, and edit files.
- Preview images, video, audio, text, Markdown, PDF, EPUB, Office-like formats,
  archives, and HTML when enabled.
- Create public shares with scoped access.
- Manage users, permissions, scopes, authentication, branding, and global
  settings from the UI or CLI.
- Run as a single binary or in containers.
- Use Redis-backed caching when configured.

## Quick Start From Source

```sh
task build
./filebrowser -r /path/to/files
```

For focused checks during development:

```sh
go test ./...
cd frontend
pnpm install --frozen-lockfile
pnpm run lint
pnpm run test
pnpm run build
```

If `pnpm` is not installed, use the version pinned in `frontend/package.json`
through `npx`, for example:

```sh
npx -y pnpm@10.33.4 install --frozen-lockfile
```

**Background:** [Goodbye File Browser, for Real This Time](https://hacdias.com/2026/07/28/filebrowser/), July 2026.

## Security

Published advisories are listed under [security advisories](https://github.com/filebrowser/filebrowser/security/advisories),
and reporting instructions are in [SECURITY.md](SECURITY.md). Two known issue classes
remain unaddressed and will not be fixed:

- **Command execution, runner, and hooks.** This feature is plagued with vulnerabilities across many published advisories, and would need a full rewrite to be made safe. It is disabled by default; if you re-enable it with `--disable-exec=false`, treat the ability to run commands as equivalent to shell access on the host. Background: [#5199](https://github.com/filebrowser/filebrowser/issues/5199).
- **Session and JWT handling.** Sessions are self-contained JWTs rather than server-side identifiers, so they cannot be revoked, which means that logout, password changes, and renewal leave previously issued tokens valid until they expire, and the same refresh token can be redeemed repeatedly. Assume a leaked token is valid until expiry. Background: [#5216](https://github.com/filebrowser/filebrowser/issues/5216).

The original upstream project recommends these safeguards, which also apply to
this fork:

- **Do not expose it directly to the internet.** Put it behind a reverse proxy that terminates TLS and performs its own authentication.
- **Keep the command runner disabled.** It is off by default, so leave it off. See [#5199](https://github.com/filebrowser/filebrowser/issues/5199) and [`docs/command-execution.md`](docs/command-execution.md).
- **Run it unprivileged, inside a container**, with only the directory you intend to serve mounted into it.

## Documentation

Documentation on how to install, configure, build, and operate the project lives
in [`docs`](docs) in this repository.

## Project Direction

This fork is intentionally pragmatic. The goal is not to turn File Browser into a
large platform; it is to keep the small, self-hosted file manager sharp:

- media workflows should feel native;
- deployment should stay simple;
- new features should remain optional and safe by default;
- upstream compatibility should be preserved unless a fork feature clearly needs
  a different path.

## Contributing

Contributions are welcome in this fork. Start with
[`CONTRIBUTING.md`](CONTRIBUTING.md), keep changes focused, and open pull
requests against this repository. The final upstream release remains the
baseline for inherited maintenance and security fixes.

## License

[Apache License 2.0](LICENSE) © File Browser Contributors

## Video link download quality

Link downloads default to **4K or better with audio** (at least 2160 pixels on both dimensions). This preset never silently falls back to a lower resolution and never upscales a source. Legacy “best” defaults migrate to this preset; custom defaults remain unchanged. Choose **Highest available** or a specific lower resolution explicitly when a source has no 4K.

Pasting a URL discovers source formats. Choices identify resolution, frame rate, dynamic range, video codec and output container, with audio details and estimated download size when provided by the source. Separate video and audio streams are merged into MKV without re-encoding; player support depends on the original codecs. Exact choices preserve the selected streams and fail if those streams become unavailable. Metadata warnings and unverified fallback presets are identified; a new URL invalidates previous results immediately. Direct file downloads retain their original quality.

Discovery and downloads use the same resolution-first sort and ignore ambient yt-dlp configuration, keeping displayed choices consistent with the requested download. Automatic updates and remote component fetching are explicitly disabled for these operations; the administrator's existing update action remains available. No optional analytics, telemetry, or tracing channel is enabled.
