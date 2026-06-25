<p align="center">
  <img src="https://raw.githubusercontent.com/filebrowser/filebrowser/master/branding/banner.png" width="550" alt="File Browser"/>
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

The upstream `filebrowser/filebrowser` project has moved into
**maintenance-only** mode. It is still an important project and may continue to
receive bug and security maintenance, but new product features are no longer the
focus there.

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

## Documentation

The original File Browser documentation is hosted at
[filebrowser.org](https://filebrowser.org). Fork-specific documentation lives in
this repository under [`www/`](www/), including notes for video thumbnails, link
downloads, and generated CLI reference pages.

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
requests against this repository. The upstream project remains the sync source
for maintenance work only.

## License

[Apache License 2.0](LICENSE) © File Browser Contributors
