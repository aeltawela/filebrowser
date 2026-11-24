# Unpatched advisories in filebrowser/filebrowser @ v2.63.23

Status in this fork: see the advisory matrix in
[`../hardening-plan.md`](../hardening-plan.md#advisory-status). Every fixed
advisory has a regression test that fails on the baseline.

- [critical] GHSA-6759-996p-gpj6 Self-signup users inherit the server root as their scope — **fixed (W1)**
- [high] GHSA-448h-jr2h-3vhp Subtitle conversion reads whole files into memory, allowing memory exhaustion — **fixed (W3)**
- [high] GHSA-c4fr-5f24-4wrj Upload failure-cleanup recursively deletes directories, bypassing Perm.Delete and deny rules — **fixed (W2)**
- [high] GHSA-8c9q-7855-wfxq Command Execution Allowlist Bypass via Shell Metacharacter Injection — **fixed (W5)**
- [high] GHSA-jvpw-637p-h3pw  Command Injection via Hook Runner — **fixed (W5)**
- [high] GHSA-xqp3-jq6g-x3qm Authentication Bypass via Proxy Auth Header Forgery — **fixed (W1)**
- [high] GHSA-7xwp-2cpp-p8r7 Insecure JWT Handling Allows Session Replay Attacks after Logout — **fixed (W1)**
- [high] GHSA-7xqm-7738-642x Uncontrolled Memory Consumption Due to Oversized File Processing — **fixed (integrator)**
- [high] GHSA-3q2w-42mv-cph4 Shell Commands Can Spawn Other Commands — **mitigated, accepted risk (W5)**
- [high] GHSA-hc8f-m8g5-8362 Command Execution not Limited to Scope — **mitigated, accepted risk (W5)**
- [medium] GHSA-39cx-23x9-5c8p Command WebSocket buffers unbounded messages before checking permissions — **fixed (W5)**
- [medium] GHSA-7w29-q235-57m9 Symlink aliases inside a user scope bypass path deny rules — **fixed (W2)**
- [medium] GHSA-8q5j-8wcr-8v2v Archive downloads hang forever on named pipes — **fixed (W3)**
- [medium] GHSA-v7vv-5wj2-gfcj Password reset does not invalidate existing JWT sessions — **fixed (W1)**
- [medium] GHSA-ffx7-75gc-jg7c TUS Negative Upload-Length Fires Post-Upload Hooks Prematurely — **regression locked (W3)**
- [low] GHSA-r6pg-pg54-rcr5 Deleting another user's shared file leaves their public share link behind — **fixed (W4)**
- [low] GHSA-m8v4-4w34-rrvf Renaming a shared file leaves the public share link behind — **fixed (W4)**
- [low] GHSA-4r8p-gqj2-mwgm Concurrent TUS uploads write past the declared Upload-Length — **fixed (W3)**
- [low] GHSA-3v48-283x-f2w4 Password Protection of Links Bypassable — **fixed (W1)**
