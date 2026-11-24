# Security Policy

This repository is a maintained fork of [File Browser](https://github.com/filebrowser/filebrowser),
which was archived upstream on 2026-09-01. The fork exists to fix the published
advisories that upstream did not address and to document the ones that cannot be
fixed safely.

See [`docs/security/hardening-plan.md`](docs/security/hardening-plan.md) for the
threat model, the invariants, and the fix and test plan.

## Supported versions

| Version | Supported |
| --- | --- |
| Fork releases after the hardening merge | ✅ |
| Upstream 2.63.x and older | ❌ |

## Reporting a vulnerability

Report privately through the fork's **Security → Advisories → Report a
vulnerability** page on GitHub. Do not open a public issue first.

Please include:

- the commit or release you tested;
- a plaintext proof of concept;
- reproduction steps;
- the affected endpoint or file, and the permissions required;
- the impact you believe it has;
- a suggested remediation, if you have one.

## Scope

In scope:

- authentication, session lifecycle, and provisioning;
- scope and rule enforcement, including symlinks and recursive operations;
- upload, archive, subtitle, and TUS resource handling;
- share-link lifecycle and password protection;
- command runner and hook hardening.

Known and accepted, not treated as vulnerabilities in this fork:

- **Command execution is not a security boundary.** A permitted program can run
  other programs and access anything the server process can access. It is
  disabled by default and requires `--enableUnsafeExec` to turn on. Reports that
  require the operator to enable it are handled as documentation issues unless
  they also affect the default configuration.
- **Application scope is not OS isolation.** File Browser confines paths inside
  the application. A process that can reach the host filesystem by another route
  is outside the application's control.
- **Authenticated resource exhaustion is bounded, not eliminated.** Limits make
  trivial attacks expensive. Rate limiting belongs at the reverse proxy.

## Deployment requirements

These are not optional for an internet-facing deployment:

- terminate TLS at a reverse proxy that performs its own authentication;
- run unprivileged, in a container, with only the served directory mounted;
- keep the command runner disabled;
- set a memory limit and restart policy on the container;
- keep the Go toolchain and dependencies patched.

See [`docs/security/hardening-plan.md`](docs/security/hardening-plan.md) for the
full list and the reasoning behind each item.
