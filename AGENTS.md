# Agent Guide

Use this file as the first stop for repository orientation before making
changes.

## Repository Shape

- Backend: Go module at the repository root.
- Frontend: Vue/Vite app in `frontend/`.
- Documentation site: MkDocs content in `www/`.
- Runtime images and container assets: `Dockerfile`, `Dockerfile.s6`,
  `compose.yaml`, and `docker/`.
- CLI documentation is generated under `www/docs/cli/`.
- Generated enum files such as `*_enum.go` should not be edited by hand unless
  regeneration is unavailable and the enum source change is intentionally kept
  in sync.

## GitHub Workflow

- Treat `origin` as the working GitHub repository for this fork.
- Use `origin` for pushes, feature branches, issues, and pull requests.
- Keep `filebrowser/filebrowser` as the upstream sync source only. Do not open
  pull requests against upstream unless the user explicitly asks for that target.
- Keep feature work in separate branches and separate pull requests.
- Merge pull requests with a squash merge so each PR lands as one commit on the
  target branch, unless the user explicitly asks for a different merge method.

## Privacy and Handoff Notes

- Do not include personal details about the requester, local machine, local
  filesystem, or local development environment in code comments, docs, commit
  messages, pull request descriptions, or final handoffs.
- Avoid local usernames, home-directory paths, absolute workspace paths,
  machine models, CPU names, OS/architecture labels from the agent host,
  timezone/location details, and screenshots or logs that reveal those values.
- Use generic deployment wording. For constrained hardware, prefer terms such as
  "low-resource PC", "small server", or "constrained device" instead of naming
  a specific board or personal device model.
- If a benchmark is useful, report the command and relative result shape without
  attaching host-identifying details. If hardware context is necessary, describe
  it generically, such as "local development machine" or "low-resource target".
- Repository owner names may appear only where required for GitHub operations;
  prefer `origin`, branch names, or pull request numbers in human-facing text.

## Development Commands

- Backend tests: `go test ./...`
- Focused backend tests: `go test ./files ./img ./http ./cmd`
- Race tests before publishing when practical: `go test --race ./...`
- Frontend install: `cd frontend && pnpm install --frozen-lockfile`
- Frontend checks: `cd frontend && pnpm run lint && pnpm run test && pnpm run build`
- Full build: `task build`

If `pnpm` is not installed locally, use the package manager version declared in
`frontend/package.json` through `npx`, for example `npx -y pnpm@10.33.4`.

## Implementation Notes

- Preserve existing functionality when optimizing. Prefer reducing unnecessary
  work, allocations, I/O, and external process calls over adding reduced-feature
  modes.
- Thumbnail generators should degrade cleanly. Missing external tools should
  fall back to existing icons or raw preview behavior instead of breaking file
  browsing.
- Keep Docker images, CLI flags, environment variables, defaults, and docs
  aligned whenever a runtime-facing option or dependency changes.
- Do not edit translation files directly for ordinary UI copy. This project
  uses Transifex for translation updates.
- Use existing helper APIs and storage abstractions before adding new patterns.
