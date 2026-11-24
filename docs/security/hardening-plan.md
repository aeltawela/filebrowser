# Security Hardening Plan

Status: implemented on `sec/integration` (see §9 for the implementation record)
Baseline: `master` after `chore: sync upstream v2.63.23`
Upstream status: archived 2026-09-01, no further fixes

## 1. Purpose

Upstream File Browser is archived. Nineteen published advisories have no patched
release, and the upstream maintainer has stated that the remaining classes cannot
be patched into shape: the session model, the authorization model, and the command
runner each need a redesign, not another guard.

This fork keeps the product usable while fixing what can be fixed properly. The
plan follows the upstream maintainer's own guidance:

- do not pretend the codebase can be made safe with isolated patches;
- fix root causes that are fixable (authorization choke point, session
  revocation, bounded inputs, provisioning);
- stop shipping unsafe features as if they were supportable (command runner,
  hook execution);
- document the deployment requirements that remain mandatory.

Two rules govern every change:

1. **Test first.** Every advisory gets a regression test that fails on the
   baseline and passes after the fix. A fix without a reproducing test is not
   accepted.
2. **One authority per decision.** A permission, scope, or rule decision is made
   once, at one choke point, on the canonical object. Handlers must not
   re-implement a weaker copy of it.

## 2. Threat model

| Trust boundary | Who crosses it | What must never happen |
| --- | --- | --- |
| Unauthenticated network | Anyone who can reach the port | Identity assertion, account creation with root scope, share password bypass |
| Authenticated low-privilege user | Default preset: create, modify, rename, delete, share, download | Reading or mutating outside scope, bypassing a deny rule, deleting a tree without `Perm.Delete`, exhausting process memory |
| Authenticated `Execute` user | Explicitly granted command execution | Spawning unlisted commands, escaping scope, injecting through filenames |
| Reverse proxy | Deployed proxy that authenticates users | Direct clients impersonating a user by setting a header |
| Shared link recipient | Anonymous holder of a public link | Access after password protection is set, access to content that replaced the shared path |

Out of scope for this plan: multi-tenant isolation against a malicious
administrator, host compromise, and side channels. Those are documented as
deployment responsibilities.

## 3. Baseline findings

The fork already contains hardening that is not in the last upstream release:
`checkDescendants`, path canonicalization for rules, case-aware rule matching,
scoped-symlink refusal, TUS length validation, and rule-path rebasing for public
shares. The plan does not re-fix those. It verifies them under regression test and
builds on them.

Verified still present at baseline:

- Self-signup with factory defaults still provisions `Scope="/"` plus the full
  destructive permission set.
- Proxy auth still trusts the configured header from any source address and
  auto-provisions accounts.
- JWTs are self-contained with no server-side revocation; logout, password
  reset, and permission changes leave old tokens valid.
- The direct-download token on a password-protected share bypasses the password.
- `POST /api/resources/<dir>?override=true` runs `RemoveAll` on failure without a
  directory guard, `Perm.Delete`, or descendant check.
- Deny rules are evaluated on the requested lexical path, not on the resolved
  symlink target.
- Archive walking opens FIFOs; subtitle conversion reads whole files.
- Concurrent TUS `PATCH` requests are not serialized.
- Rename does not purge shares; delete purges only the deleter's shares.
- `/api/command` reads an unbounded WebSocket message before authorization.
- Hook execution expands `$FILE`/`$USERNAME` into shell arguments without
  escaping.

## 4. Security invariants

These become the review checklist. Every handler and filesystem operation must
satisfy them.

**I1 — Scope.** No path operation resolves outside the user's scope. Symlinks
are resolved before the decision, not after.

**I2 — Rules.** A deny rule is enforced on the object that is actually opened,
modified, or removed. If `A` is an alias of `B`, a rule that denies `B` denies
`A`.

**I3 — Permission.** The permission checked matches the effect. Recursive
operations require the recursive permission and re-check every descendant.
Cleanup paths inherit the same constraints as user-initiated deletes.

**I4 — Boundedness.** Every user-controlled size, count, duration, or offset is
bounded before allocation or blocking syscall: request bodies, WebSocket
messages, subtitle/archive inputs, TUS lengths, archive entry counts, thumbnail
dimensions.

**I5 — Revocability.** Authentication state can be invalidated server-side.
Logout, password change, permission change, and account disable take effect
immediately for all previously issued tokens.

**I6 — Identity provenance.** A username from a header is trusted only when the
request comes from a configured trusted proxy, and provisioning never grants
more than the configured least privilege.

## 5. Workstreams

File ownership is exclusive per workstream to keep branches mergeable. A
workstream that needs a change in another's file must request it in the plan
instead of editing it.

### W1 — Authentication, provisioning, sessions (5 advisories)

Owner: Hy4 flash Free
Files: `auth/`, `http/auth.go`, `users/`, `storage/bolt/users.go`,
`settings/settings.go`, `settings/dir.go`, `settings/user.go`, `cmd/root.go`,
`cmd/config.go`, `http/http.go` (logout route only), `frontend/src/utils/auth.ts`.

| Advisory | Fix |
| --- | --- |
| GHSA-6759-996p-gpj6 (critical) | Provisioning from an untrusted flow must derive a per-user scope or be refused. If `createUserDir=false` and the default scope is root, `POST /api/signup` returns `403` and logs a configuration error. Signup never inherits `Create`, `Modify`, `Delete`, `Rename`, `Share` from defaults unless a new explicit `signupPermissions` block is set; the safe default is `download` only. |
| GHSA-xqp3-jq6g-x3qm (high) | Add `auth.proxy.trustedProxies` (CIDR list) and an optional `auth.proxy.secret` header. Reject the header when `RemoteAddr` is not trusted, when both a trusted source and secret are unconfigured, or when a secret is configured and does not match. Refuse to start (not just warn) when `auth.method=proxy` and neither control is configured, unless `auth.proxy.allowUntrusted=true` is explicitly set. Auto-provisioning requires `auth.proxy.createUser=true` and always derives a scope. |
| GHSA-7xwp-2cpp-p8r7 (high) | Add `TokenVersion` to the user record and a `ver` claim to the JWT. `withUser` rejects a token whose version differs. Bump on logout, password change, permission change, and admin disable. Add `POST /api/logout`; make the frontend call it before clearing local state. |
| GHSA-v7vv-5wj2-gfcj (medium) | The same version bump is applied by the user-update handler whenever `Password`, `Perm`, `LockPassword`, or `Scope` changes, including admin-initiated resets. |
| GHSA-3v48-283x-f2w4 (low) | Bind the direct-download token to the share's password state: include the password hash and expiry in the HMAC input, and refuse a token-only request when the link is password-protected and no password was verified. Document that the "download link" is not a bearer bypass. |

Design notes:

- `TokenVersion` is additive with a zero default so existing users and tokens
  keep working until their next invalidation. A token without `ver` is treated as
  version 0 only when the stored version is 0; otherwise it is rejected.
- Revocation is a store write plus a check on every request. Do not cache user
  records for longer than the request.
- `renew` must not silently extend a revoked session: it re-reads the user and
  re-checks the version.

### W2 — Path and upload authorization (2 open + 3 regression)

Owner: Iceman Contributor Free
Files: `files/`, `rules/`, `http/data.go`, `http/resource.go`.

| Advisory | Fix |
| --- | --- |
| GHSA-c4fr-5f24-4wrj (high) | Reject an upload target that is an existing directory with `400` before writing. Replace the failure `RemoveAll` with removal of a temp file created by this request. Implement uploads as write-to-temp + `fsync` + `rename` in the target directory, so a failed upload cannot remove pre-existing content and an interruption cannot leave a truncated file. |
| GHSA-7w29-q235-57m9 (medium) | Make rule evaluation follow the same resolution as the filesystem. Add `ScopedFs.ResolveVirtual(name)` returning the scope-relative path with symlinks resolved, and have `data.Check` evaluate rules against it. Pass the checker into `ScopedFs` (constructor variant) so opens, writes, and removes cannot skip the check even if a handler forgets. Keep the lexical check as a cheap first pass. |
| GHSA-77x8-73f4-5485 (patched) | Keep `checkDescendants`; extend it to resolved paths so a symlinked descendant cannot hide a denied target. Add regression tests. |
| GHSA-fmm7-x4gx-8jhr (patched) | Add a regression test for upload cleanup through a symlink, using the new temp-file flow. |
| GHSA-8wc8-hf36-mjh9 (patched) | Add a regression test for dangling-symlink writes. |

Design notes:

- Resolution must not itself break legitimate operations on paths that do not
  exist yet. `ResolveVirtual` walks to the nearest existing ancestor, matching
  the existing `within` semantics, and returns a virtual path.
- `FollowExternalSymlinks=true` keeps its documented meaning, and deny rules
  still apply to any resolved target that has a virtual (in-scope)
  representation. A target outside the scope has no virtual path for a rule
  to name; that mode is accepted risk (fail-open by configuration), and the
  default symlink-confined mode fails closed instead.
- Temp files use a random suffix and are removed on every error path. Rename is
  in the same directory, so it is atomic on POSIX and filesystems that support
  it.

### W3 — Bounded inputs: subtitle, archives, TUS (4 advisories)

Owner: Muse Spark 1.3 Contributor
Files: `http/subtitle.go`, `http/raw.go`, `http/tus_handlers.go`,
`http/upload_cache*.go`, `files/file.go` (limit constants only), `settings/settings.go`
(new limit fields only, coordinated with W1).

| Advisory | Fix |
| --- | --- |
| GHSA-448h-jr2h-3vhp (high) | Reject subtitle files above a configurable limit (default 4 MiB) with `413` before reading. Bound the SSA reader with `io.LimitReader`, and cap the rendered WebVTT buffer. |
| GHSA-8q5j-8wcr-8v2v (medium) | Skip non-regular files (FIFO, socket, device) in the archive walk. Open regular files only. Apply the same guard in `rawFileHandler` for public single-file shares. Bound total archived bytes and entry count, and return a clear `413` when exceeded. |
| GHSA-4r8p-gqj2-mwgm (low) | Serialize TUS `PATCH` per upload with a keyed lock. Re-stat and compare the offset while holding the lock, write at most `length-offset` bytes, and make completion idempotent so the hook fires once. |
| GHSA-ffx7-75gc-jg7c (patched) | Keep the negative-length rejection, add an explicit test, and reject negative offsets as well. |

Design notes:

- Limits live in settings with conservative defaults and are exposed in
  `config` output, not hidden constants. This follows the repository rule that
  runtime-facing options stay aligned with docs and CLI.
- Archive limits are enforced while walking, before the archiver is invoked, so
  the error is deterministic.
- The TUS lock map is bounded by the upload cache lifecycle; entries are
  removed when the upload completes or is deleted.

### W4 — Share lifecycle (2 advisories)

Owner: Muse Spark 1.3 Contributor (second batch, after W3)
Files: `http/share.go`, `http/public.go`, `http/resource.go` (rename cleanup
call only, coordinated with W2), `share/`, `storage/bolt/share.go`.

| Advisory | Fix |
| --- | --- |
| GHSA-m8v4-4w34-rrvf (low) | After a successful rename, delete shares whose path is the source or a descendant. Do not re-point them: a moved file must not inherit an old public link. |
| GHSA-r6pg-pg54-rcr5 (low) | Separate filesystem-event cleanup from the share-management API. `DeleteByPath` (internal, unfiltered) runs when a file is deleted; `DeleteWithPathPrefix(path, userID)` remains filtered for the API so cross-user share deletion stays impossible. |

Design notes:

- Cleanup runs after the filesystem operation succeeds, and failures are logged
  but do not fail the user request.
- A share pointing at a missing path must never become valid again when a new
  file appears. Add an optional path fingerprint (size + modtime at creation) and
  refuse to serve when it no longer matches; this is defense in depth for
  cleanup failures and crash windows.

### W5 — Command runner and hooks (5 advisories, accepted risk)

Owner: TBD after W1–W4 review
Files: `http/commands.go`, `runner/`, `auth/hook.go`, `docs/command-execution.md`,
`cmd/root.go`.

The allowlist model cannot be fixed. A permitted binary that can execute other
programs (`find`, `git`, `env`, `sed`, interpreters) defeats any list. The fork
therefore does not claim this feature is safe:

- Keep it disabled by default and require an explicit second acknowledgement
  (`--enable-unsafe-exec`) in addition to `--disable-exec=false`.
- Check `EnableExec` and `Perm.Execute` immediately after the WebSocket upgrade,
  before reading a message, and set a 64 KiB read limit.
- Remove shell mode from the allowlist path: when `Shell` is configured, reject
  metacharacters and pass the command as a single argument only if the configured
  shell is known to treat it literally, or refuse the combination. Document the
  decision.
- Stop expanding hook variables into arguments. Pass `FILE`, `SCOPE`,
  `TRIGGER`, `USERNAME`, and `DESTINATION` as environment variables only, and run
  hooks with a direct `exec.Command` argv.
- Document that enabling the runner is equivalent to shell access and that scope
  is not an OS boundary.

### W6 — Verification, docs, release gates

Owner: final reviewer Hy4
Files: `docs/security/`, `SECURITY.md`, `README.md`, `.github/workflows/`.

- Publish an advisory matrix with status per GHSA: fixed, mitigated, accepted.
- Update `SECURITY.md` with the fork's reporting policy and supported versions.
- Add a startup self-check that refuses unsafe configurations: proxy auth
  without trust anchors, signup without derived scope, exec enabled without the
  acknowledgement.
- CI: `go test -race ./...`, `go vet ./...`, `govulncheck ./...` (non-blocking
  for toolchain CVEs that require a Go bump).
- Release checklist requires every regression test from this plan to pass.

## 6. Test plan

Each test names the advisory it protects and must fail on baseline. Unit tests
live next to the code; HTTP-level tests use the existing helpers in `http/`.

| Test | Advisory | Assertion |
| --- | --- | --- |
| `TestSignupRequiresDerivedScope` | GHSA-6759-996p-gpj6 | Signup with `createUserDir=false` is refused; no user with scope `/` is created |
| `TestSignupLeastPrivilege` | GHSA-6759-996p-gpj6 | Self-registered user has download only unless configured otherwise |
| `TestProxyAuthRejectsUntrustedPeer` | GHSA-xqp3-jq6g-x3qm | Header from a non-trusted address is not authenticated |
| `TestProxyAuthRequiresTrustConfig` | GHSA-xqp3-jq6g-x3qm | Startup refuses proxy auth without trust anchors |
| `TestProxyAuthNoImplicitProvisioning` | GHSA-xqp3-jq6g-x3qm | Unknown username is not created unless enabled |
| `TestLogoutRevokesToken` | GHSA-7xwp-2cpp-p8r7 | Old token returns `401` after logout |
| `TestPasswordResetRevokesToken` | GHSA-v7vv-5wj2-gfcj | Old token returns `401` after an admin reset |
| `TestTokenVersionMismatchRejected` | both session items | Forged/stale `ver` claim is rejected |
| `TestSharePasswordNotBypassedByDownloadToken` | GHSA-3v48-283x-f2w4 | Token alone cannot fetch a password-protected share |
| `TestUploadToDirectoryRejected` | GHSA-c4fr-5f24-4wrj | `POST` to a directory is `400`, tree intact |
| `TestFailedUploadDoesNotDeleteExistingTree` | GHSA-c4fr-5f24-4wrj | Failed upload leaves pre-existing files and denied descendants |
| `TestUploadCleanupDoesNotBypassDeletePerm` | GHSA-c4fr-5f24-4wrj | Create+modify user cannot delete via upload failure |
| `TestDenyRuleAppliesToSymlinkTarget` | GHSA-7w29-q235-57m9 | Read and write through an in-scope alias to a denied path return `403` |
| `TestSymlinkedDescendantBlocksRecursiveOp` | GHSA-77x8-73f4-5485 | Copy/rename/delete refuse when a resolved descendant is denied |
| `TestSubtitleSizeLimit` | GHSA-448h-jr2h-3vhp | Oversized `.srt`/`.ass` returns `413` without full read |
| `TestArchiveSkipsNamedPipe` | GHSA-8q5j-8wcr-8v2v | Archive of a directory containing a FIFO completes and omits it |
| `TestPublicShareNamedPipeDoesNotHang` | GHSA-8q5j-8wcr-8v2v | Public single-file share of a FIFO returns promptly |
| `TestArchiveLimits` | GHSA-8q5j-8wcr-8v2v | Entry/byte limit returns `413` |
| `TestTUSConcurrentPatchesRespectLength` | GHSA-4r8p-gqj2-mwgm | Final size equals `Upload-Length`; one hook |
| `TestTUSRejectsNegativeLengthAndOffset` | GHSA-ffx7-75gc-jg7c | `400` and no hook |
| `TestRenamePurgesShares` | GHSA-m8v4-4w34-rrvf | Old link 404s and stays 404 after a new file appears |
| `TestDeletePurgesOtherUsersShares` | GHSA-r6pg-pg54-rcr5 | Deleted path's link is gone regardless of deleter |
| `TestShareFingerprintMismatch` | both share items | Share refuses to serve a different object at the same path |
| `TestCommandWSRejectsOversizedMessage` | GHSA-39cx-23x9-5c8p | Message above the limit closes the connection before allocation |
| `TestCommandWSRequiresPermissionBeforeRead` | GHSA-39cx-23x9-5c8p | Unauthorized client cannot send a payload that is buffered |
| `TestHookFilenameNotInterpreted` | GHSA-jvpw-637p-h3pw | Filename `; id #` is passed verbatim as an env value |
| `TestCommandAllowlistRejectsMetacharacters` | GHSA-8c9q-7855-wfxq | Shell metacharacters are rejected or never reach a shell |
| `TestExecRequiresAcknowledgement` | GHSA-3q2w-42mv-cph4, GHSA-hc8f-m8g5-8362 | Enabled exec without acknowledgement refuses to start |

## 7. Merge and review gates

1. Branch per workstream: `sec/w1-auth-sessions`, `sec/w2-path-uploads`,
   `sec/w3-limits`, `sec/w4-shares`, `sec/w5-exec`.
2. Each branch must pass `go build ./...`, `go test ./...`, and
   `go test -race ./...` for touched packages.
3. Reviewer checks invariants I1–I6 against the diff, not just the tests.
4. Merge order: W2, W3, W4, W1, W5. W1 touches route registration; W4 needs
   W2's rename call site. Conflicts are resolved by the integrator, never by
   rewriting another workstream's fix.
5. After merge: full test run, manual smoke test of login, upload, download,
   share, rename, delete, archive, subtitle, and TUS resume.
6. Tag a fork release with the advisory matrix and upgrade notes.

## 8. Accepted risk and non-goals

- **Command runner.** Not made safe. Enabled only with an explicit
  acknowledgement, documented as shell-equivalent, excluded from the security
  claim. The only real fix is removing the feature or running each scope under a
  distinct UID.
- **Session storage.** Token versioning gives revocation without a session
  table. It does not give per-device sessions, rotation with reuse detection, or
  immediate expiry of every outstanding token. A server-side session store is the
  follow-up architecture change.
- **OS-level isolation.** Scope is an application concept. A process that can
  read the host filesystem through another path is outside what this code can
  prevent. Deploy unprivileged, in a container, with only the served directory
  mounted, behind a TLS-terminating proxy.
- **Denial of service by an authenticated user.** Bounds make trivial exhaustion
  expensive; they do not make the server immune. Rate limiting belongs at the
  proxy.
- **Third-party parsers.** EPUB, EXIF, video thumbnails, and archives can still
  contain parser bugs. Keep the toolchain patched, and run with memory limits.

## 9. Implementation record

All five workstreams are implemented and merged into `sec/integration`. Every
regression test below was confirmed to fail on the baseline (or on the
pre-fix code) and to pass after the fix. Full `go test ./...` and
`go test -race ./...` pass on the integrated branch.

### Advisory status

| Advisory | Status | Workstream | Regression tests |
| --- | --- | --- | --- |
| GHSA-6759-996p-gpj6 | fixed | W1 | `TestSignupRequiresDerivedScope`, `TestSignupLeastPrivilege`, `TestSignupPermissionsOptIn`, `TestCreateUserHomeRefusesRootScope`, `TestSignupStartupRefusesRootScope` |
| GHSA-xqp3-jq6g-x3qm | fixed | W1 | `TestProxyAuthRejectsUntrustedPeer`, `TestProxyAuthRequiresTrustConfig`, `TestProxyAuthNoImplicitProvisioning`, `TestProxyAuthRequiresSecret`, `TestProxyProvisioningRefusesRootScope`, `TestProxyAuthTrustConfigFlags` |
| GHSA-7xwp-2cpp-p8r7 | fixed | W1 | `TestLogoutRevokesToken`, `TestRenewDoesNotResurrectRevokedToken`, `TestTokenVersionMismatchRejected` |
| GHSA-v7vv-5wj2-gfcj | fixed | W1 | `TestPasswordResetRevokesToken`, `TestIncrementTokenVersion`, `TestUpdateRevokesOnSensitiveChanges` |
| GHSA-3v48-283x-f2w4 | fixed | W1 | `TestSharePasswordNotBypassedByDownloadToken` |
| GHSA-c4fr-5f24-4wrj | fixed | W2 | `TestUploadToDirectoryRejected`, `TestFailedUploadDoesNotDeleteExistingTree`, `TestUploadCleanupDoesNotBypassDeletePerm` |
| GHSA-7w29-q235-57m9 | fixed | W2 | `TestDenyRuleAppliesToSymlinkTarget`, `TestSymlinkedDescendantBlocksRecursiveOp` |
| GHSA-448h-jr2h-3vhp | fixed | W3 | `TestSubtitleSizeLimit` |
| GHSA-8q5j-8wcr-8v2v | fixed | W3 | `TestArchiveSkipsNamedPipe`, `TestPublicShareNamedPipeDoesNotHang`, `TestArchiveLimits` |
| GHSA-4r8p-gqj2-mwgm | fixed | W3 | `TestTUSConcurrentPatchesRespectLength` |
| GHSA-ffx7-75gc-jg7c | regression locked | W3 | `TestTUSRejectsNegativeLengthAndOffset` (negative-length rejection already present; negative offset added) |
| GHSA-m8v4-4w34-rrvf | fixed | W4 | `TestRenamePurgesShares`, `TestRenamePurgesDescendantShares` |
| GHSA-r6pg-pg54-rcr5 | fixed | W4 | `TestDeletePurgesOtherUsersShares`, `TestDeleteByPathPrefixIgnoresOwner` |
| GHSA-39cx-23x9-5c8p | fixed | W5 | `TestCommandWSRejectsOversizedMessage`, `TestCommandWSRequiresPermissionBeforeRead`, `TestCommandWSDisabledRejectsBeforeRead` |
| GHSA-8c9q-7855-wfxq | fixed | W5 | `TestParseCommandDoesNotUseShell`, `TestParseCommandKeepsArgv` |
| GHSA-jvpw-637p-h3pw | fixed | W5 | `TestHookFilenameIsNotInterpretedAsShellSyntax` |
| GHSA-3q2w-42mv-cph4 | mitigated, accepted risk | W5 | `TestValidateUnsafeExecRequiresAcknowledgement` |
| GHSA-hc8f-m8g5-8362 | mitigated, accepted risk | W5 | `TestValidateUnsafeExecRequiresAcknowledgement` + `docs/command-execution.md` |
| GHSA-7xqm-7738-642x | fixed | integrator | `TestTextReadBoundedWhenFileGrowsAfterStat`, `TestReadTextContentBounded`, `TestDetectTypeLargeFileNotText` |

Previously patched advisories kept under regression: GHSA-77x8-73f4-5485
(`TestSymlinkedDescendantBlocksRecursiveOp`), GHSA-fmm7-x4gx-8jhr
(`TestUploadThroughSymlinkCleanup`), GHSA-8wc8-hf36-mjh9
(`TestDanglingSymlinkWriteStaysInScope`), GHSA-v3jv-rmh2-635j (expired JWT
waiver under proxy auth; `TestExpiredTokenNeedsProxyAssertion`, extended with
an untrusted-peer rejection case for the new trust anchor).

### Deviations from the design

1. **W3 limits are package constants, not settings fields.** The bounds
   (4 MiB subtitle input, 8 MiB subtitle output, 10,000 archive entries,
   1 GiB archived bytes) are conservative and unconditional. Making them
   configurable is a follow-up; a knob that can be raised to "unlimited"
   would weaken the invariant without adding safety.
2. **W4 path fingerprint not implemented.** Share cleanup on delete, rename,
   and overwrite covers both advisories. The optional size+modtime fingerprint
   (defense in depth for a crash between the filesystem op and the cleanup)
   remains a follow-up; the residual window is documented here.
3. **W5 removed shell insertion entirely** for both interactive commands and
   hooks, instead of rejecting metacharacters. This is stronger than the
   design: hook values reach the process only through the environment, and a
   configured `shell` setting is ignored (with a startup warning). The
   acknowledgement flag is `--enableUnsafeExec` (camelCase, matching the
   repository's flag convention). Quick-setup users no longer receive
   `Execute` by default.
4. **W1 share download token** is a 1-hour HMAC capability bound to the
   share's password hash and expiry, minted only after a password check. The
   stored random token is never accepted as a bearer credential. Download
   URLs copied before this change stop working by design; the recipient must
   re-enter the password.
5. **Merge order** was W3 → W4 → W5 → W1 → W2 (W2 finished last). Conflicts
   were limited to `cmd/root.go` (two startup gates) and `http/resource.go`
   (share cleanup + temp-file helpers) and were resolved by keeping both
   sides; no workstream fix was rewritten.
6. **GHSA-7xqm residual closed by the integrator.** W3 reported the
   stat-to-read race in the `X-Encoding: true` path as out of its scope; the
   fix bounds that read (and the `detectType` content reads) with
   `files.MaxTextContentBytes` and fails closed with `413`/error.
7. **W6 startup gates** are enforced for all three unsafe configurations:
   proxy auth without trust anchors, exec without acknowledgement, and signup
   without a derivable scope. CI runs `go vet`, `go test -race ./...`, and a
   non-blocking `govulncheck`.

### Known residuals (accepted, documented)

- Command execution remains shell-equivalent when explicitly enabled (§8).
- A crash between a filesystem operation and share cleanup can leave a stale
  link until the next operation at that path; the fingerprint follow-up would
  close this.
- Recursive operations walk the lexical tree; a symlink to an allowed
  directory whose descendants are denied is caught when the resolved target
  is checked, but deeper traversal semantics may need a broader design.
- Token revocation is per-user, not per-device; a server-side session store
  remains the follow-up architecture change.

## 10. Final-review follow-ups

An independent review of the integrated diff (invariants I1–I6, full and race
suites green) returned **request changes** with one blocker and several minors.
All are fixed on `sec/integration`; each fix has a regression test that fails
without it.

- **Hook auth reset the session epoch (blocker, I5).** `HookAuth.GetUser`
  rebuilds the user without `TokenVersion`, and the storage choke point
  persisted the caller's zero on full saves — undoing logout/password-reset
  revocation on the next login, and minting stale tokens after a revoking
  login. `TokenVersion` is now server-owned in `users.Storage.Update` and
  `users.Storage.Save`: the stored epoch is always preserved, sensitive
  changes still bump it, and the caller's object is refreshed from the store
  so minted tokens carry the live epoch. Tests:
  `TestHookAuthLoginPreservesAndAdvancesEpoch` (login → logout → benign
  re-login keeps the old token dead; scope-changing login mints a working
  token), `TestFullSavePreservesTokenVersion`,
  `TestSaveOverExistingPreservesEpochAndRevokes` (covers
  `users import --overwrite`, which previously bypassed the choke point).
- **Proxy renewal trusted the bare header (minor, I6).**
  `proxyAsserts` (the expiry waiver for proxy auth with a custom logout page)
  never checked the trust anchor. It now requires `ProxyAuth.Trusted(r)`.
  Test: `TestProxyAssertsRequiresTrustAnchor`.
- **Hook explicit root scope (minor, behavior).** `CreateUserHome` refused an
  explicitly provided root scope, breaking the documented hook example. An
  explicit scope is admin intent (only hook auth passes one) and is now
  accepted; the refusal applies to implicit provisioning only (signup, proxy
  auto-provisioning). Test: `TestCreateUserHomeAllowsExplicitRootScope`.
- **Share token now also binds password changes (test gap).** The expiry
  binding was tested; the password-hash binding was only code. Extended
  `TestSharePasswordNotBypassedByDownloadToken` to rotate the password.
- **Status-code and doc nits.** `ErrTextTooLarge` (stat→read grow race) now
  maps to `413` instead of `500` (`TestDetectTypeGrowRaceReturns413`); the
  misleading shell warning now says the configured shell is ignored; delete
  cleanup runs after the `RemoveAll` succeeds; the archive `Open` callback
  charges post-walk growth against the byte budget; the subtitle output cap
  has a direct test (`TestSubtitleOutputCap`).

- **Epoch read-modify-write TOCTOU (follow-up hardening, I5).** Storm's `save`
  is a blind `bucket.Put` overwrite in one transaction, but the choke-point
  sequences span several transactions (read epoch → write record → bump), so
  a full save that read the epoch before a concurrent revocation could write
  the stale epoch back over the bump. `users.Storage` now serializes the
  per-user sequences (`Update`, `Save`, `IncrementTokenVersion`) with a
  striped lock array; backend transactions stay as they are. Test:
  `TestConcurrentSavesAndRevocationsKeepEpochMonotonic` (trips the race
  detector without the locks, passes with them).

## 11. Final-form review by all four reviewers

After the §10 follow-ups, the final form was reviewed independently by all
four model reviewers. Verdicts:

- **Hy4 (final): APPROVE** — no blocking or required items. Verified the
  blocker, all minors, and the epoch TOCTOU close-out; confirmed via scratch
  revert that 5 tests fail without their fixes; ran full + race suites green.
  Two informational notes, no action: `Delete` is intentionally outside the
  epoch stripe lock (deletions fail closed at `Get`), and the archive `Open`
  delta conservatively double-counts the entry slot (fixed below as #1).
- **Hy4 flash Free (W1/auth surface): APPROVE** — no open auth bypass, epoch
  forgery, trust-anchor gap, provisioning hole, or session revival. One
  security-relevant config trap fixed (#1 below); the rest is polish.
- **Muse Spark 1.3 Contributor (limits/shares/exec/docs): APPROVE** — no
  bound bypass, hang, share forgery, or exec escape. Findings #1–#8 below.
- **Iceman Contributor Free (W2/path surface): APPROVE** — no scope escape,
  deny bypass, cleanup data loss, or temp race. One false-deny fixed below.

Fixes applied after (each with a failing-before/passing-after test):

1. `config set` can now clear proxy trust settings (`flags.Changed` gates;
   an explicit empty/false clears instead of silently keeping the stored
   value). Proxy `secret` (and nested reCAPTCHA secret) redacted from
   `config cat`; `allowUntrusted` help notes it is inert while anchors are
   configured; token for a deleted user returns 401, not 500.
2. Archive `Open` growth charges bytes only (`addBytes`; no entry
   double-count).
3. Share list expiry purge filters into a fresh slice (consecutive expired
   links were skipped and over-reported as valid).
4. `ResolveVirtual` keeps the tail below the nearest existing ancestor in
   both resolvers, and `guard` uses the same tail-aware path — a global
   deny / with a user allow /allowed no longer refuses creation under a
   not-yet-existing allowed subtree.
5. TUS override-POST holds the per-upload lock across truncate + re-register.
6. `docs/command-execution.md` states the exact two-opt-in semantics.

Accepted residuals (documented, not fixed):

- TUS lock is per-process and keyed by virtual path: multi-replica
  deployments need sticky sessions or an external lock, and two virtual
  aliases to one real path do not share a lock. Narrow, single-instance
  proven by the race test.
- `fileutils.MoveFile` cross-device fallback and `CopyFile` truncate-then-copy
  can lose the destination on mid-copy failure (pre-existing, unchanged by
  this fork; rename/copy require the matching permissions and pass rule
  checks first).
- A no-output allowlisted command with a disconnected client runs to exit
  (pre-existing; consistent with exec being shell-equivalent when enabled).
- Share HMAC reuses the JWT signing key with domain-separated inputs; no
  practical cross-protocol forgery, but purpose-separated keys would be
  cleaner.
- `.vtt` passthrough re-reads up to 4 MiB+1 before re-serving from the
  descriptor (wasteful, verified correct output).
- `signupPermissions` has no `config set` flag surface; the `config import`
  path is documented in `docs/authentication.md`.

## 13. Post-merge incident and second review round

A `git add -A` in commit `73e0be0c` accidentally staged a reviewer's live
scratch-revert of `http/public.go`, recommitting the stored-token bypass
(GHSA-3v48-283x-f2w4) on `master`. The full suite caught it immediately
(`TestSharePasswordNotBypassedByDownloadToken`), and two independent
reviewers confirmed the exact hunk. Lessons, now enforced: never `git add -A`
while reviewers scratch-test in the same checkout (verify `git status` in
full first), and require green targeted tests before every commit. Reviewers
were moved to isolated worktrees for scratch verification afterwards.

Fixes from the second review round (each with a failing-before test):

- Restored the HMAC download-token verification (blocker).
- `writeFile` preserves the destination's permission bits (`.Perm()`), with a
  regression test proving overwrite no longer strips the execute bit.
- Hook provisioning strips `Admin`/`Execute`/commands from defaults unless
  explicitly returned by the hook, mirroring signup and proxy provisioning.
- TUS abort purges shares and runs the delete hook, like a regular delete;
  override-POST already required `Modify`, and a create-only TUS flow test
  pins that new uploads stay completable without it.
- Non-regular downloads answer 403 instead of an empty 200.
- TUS length arithmetic guards `math.MaxInt64` overflow; reCAPTCHA secret is
  clearable via `config set`; startup warns that deny rules cannot cover
  external targets under `FollowExternalSymlinks`.
- `writeFile`'s parent-creating `MkdirAll` predates this fork (baseline
  behavior) and is kept intentionally: uploads create missing parents inside
  the scope, still rule-checked.

## 14. External corroboration and adopted improvements

Web research (Ollama hosted search) surfaced two active continuation forks
whose published hardening notes overlap this plan and each other:

- FilebrowserNext/filebrowserNEXT v3 (active): BoltDB revocation store with
  TTL caching, logout revocation, targeted invalidation on credential changes
  (password, permissions, username, scope, rules) while cosmetic saves do not
  disconnect, login/share-password rate limiting (10 per IP per 5 minutes,
  HTTP 429), proxy-header trust restricted to private/loopback peers, global
  security headers, command-runner confinement, single-use renewal via `jti`,
  deleted-user 401, case handling and whitespace trimming.
- FileBrowser Quantum docs: proxy group-claims, `trustProxyHeaders` bound to
  loopback-only exposure, public/private route split with hash-based share
  auth, authentication rate limiting.

Adopted from that comparison (each with a failing-before test):

- Failed-login and share-password rate limiting (10 per key per 5 minutes,
  429), keyed on the TCP peer — never on forwarded headers.
- Global `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
  `Referrer-Policy`, and `Permissions-Policy` response headers.
- Access-rule changes revoke outstanding tokens (the sensitive set is now
  Password, Perm, LockPassword, Scope, Rules).

Considered and deliberately not adopted: single-use renewal via a `jti`
revocation store (needs a new server-side store — the documented session-table
follow-up); revocation on username renames (tokens bind the immutable user ID,
so a rename alters nothing authorized); trusting `X-Forwarded-For` from
private peers (rejected — ours is stricter by design); command blocklists
(rejected — direct-argv execution already neutralizes metacharacters without
a bypassable list); case-insensitive login and whitespace trimming (UX, out
of scope); the DNSGeek module-rename fork (structural, no security content
to adopt).

## 15. Second review round (all free models) and external corroboration

All 12 free-suffixed models were asked for an independent full-diff review;
9 ran (3 blocked on shared free-quota exhaustion — deepseek, mimo,
nemotron-vl — and are recorded as not covered). Verdicts: 7 approve outright
(Hy4 final, W1, W2, Spark, nemotron-lightning, tidepool, flint), 3
request-changes with findings that were all fixed and re-verified (beam-go
and beam-op: the committed share-token revert, fixed by restoring the HMAC
check; hy4-flash-free: file-mode reset on overwrite, logout liveness, doc
wording). Iceman's two minors (epoch read-window atomicity, TUS delete side
effects) and tidepool's nits (regex-raw revocation, TUS length-before-truncate,
checker mutex, fail-closed GetBy, assertion hardening) are fixed below.

External corroboration (Ollama hosted web search) surfaced FilebrowserNext v3
and FileBrowser Quantum, whose published hardening matches this plan's
invariants and contributed four adoptions: failed-login/share-password rate
limiting (10/key/5min, 429), global security headers, rule-change revocation,
and the deleted-user 401 (already present). Deliberately not adopted: jti
single-use renewal (needs a session store — the documented follow-up),
username-rename revocation (tokens bind the immutable ID), X-Forwarded-For
trust (rejected — stricter by design), command blocklists (direct-argv is
stronger), login normalization (UX, out of scope).

Process note: a `git add -A` once staged a live scratch-revert. Since then:
no `git add -A` (explicit paths, full `git status` review first), green
targeted tests before every commit, and scratch testing in isolated
worktrees.

## 16. How others fixed it (corroboration)

- Upstream's final fixes (PR #6053, merged 2026-07-27, in our baseline):
  `checkDescendants` for recursive ops (kept and extended to resolved paths
  here) and the expired-proxy-token waiver gated on the proxy header still
  naming the user. Our W1 keeps that waiver but additionally requires the
  configured trust anchor (CIDR/secret), closing the spoofed-header path
  upstream left open.
- Active forks find the same classes: gtsteffaniak/filebrowser publishes its
  own GHSA series (Nov 2026) for symlink scope/rule bypasses, archive symlink
  writes, and deny-rule bypass via filesystem name aliases — the same three
  families this plan fixes at the choke point (resolved-path rules, temp-file
  uploads, archive FIFO/budget guards). Their remaining items (watcher,
  WebDAV, quotas, API tokens, cross-source view grants) belong to features
  this v2 codebase does not have and are not applicable here.
- The DNSGeek/filebrowser v3 module rename referenced in #5199/#5216 is
  structural, with no security content to adopt.
