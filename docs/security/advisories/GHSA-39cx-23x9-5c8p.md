# GHSA-39cx-23x9-5c8p / CVE-2026-90927 — Command WebSocket buffers unbounded messages before checking permissions

- Severity: medium (CVSS 6.5)
- CWEs: CWE-400
- Affected: github.com/filebrowser/filebrowser/v2 <= 2.63.23
- Published: 2026-08-31T06:27:00Z
- URL: https://github.com/filebrowser/filebrowser/security/advisories/GHSA-39cx-23x9-5c8p

---

## Summary

The `/api/command` WebSocket handler reads a complete client message before checking either
`EnableExec` or the user's `Perm.Execute` permission, and never calls `SetReadLimit`. Any authenticated
user who can reach the endpoint — including one with no command-execution permission, on a server where
the command feature is disabled — can make the process allocate and retain an arbitrarily large
WebSocket message before the request is rejected.

Verified at v2.63.23, the final release. The handler has had this shape since v2.0.0.

## Details

`commandsHandler` upgrades the connection and reads a full message before any authorization check:

```go
// http/commands.go:41-68
var commandsHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	conn, err := upgrader.Upgrade(w, r, nil)
	...
	for {
		_, msg, err := conn.ReadMessage()
		...
		raw = strings.TrimSpace(string(msg))
		if raw != "" {
			break
		}
	}

	// Fail fast
	if !d.server.EnableExec || !d.user.Perm.Execute {
```

No `conn.SetReadLimit` call occurs anywhere in the codebase, and gorilla/websocket applies no default
bound: its size guard is `if c.readLimit > 0 && c.readLength > c.readLimit` (`conn.go:924`), so the
zero-value limit disables the check entirely. `ReadMessage` then buffers the whole message with
`ioutil.ReadAll` (`conn.go:1091-1099`). The `ReadBufferSize: 1024` on the upgrader
(`http/commands.go:22-25`) sizes the underlying read buffer only; it does not bound the assembled
message. `string(msg)` copies the buffer a second time before the comparison.

The route is registered unconditionally, so disabling the command feature does not remove it:

```go
// http/http.go:88
api.PathPrefix("/command").Handler(monkey(commandsHandler, "/api/command")).Methods("GET")
```

`commandsHandler` is the only WebSocket handler in the codebase; the other endpoints are plain HTTP and
the JSON auth routes are already bounded by `maxAuthBodySize` (`http/auth.go:24,159,193`).

## PoC

Against revision `833d9088`, as a user with empty permissions and a server built with
`EnableExec: false`, a 32 MiB message is fully buffered before the handler answers
`Command not allowed.`:

```go
payload := bytes.Repeat([]byte("A"), 32<<20)
if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
	t.Fatal(err)
}

_, response, err := conn.ReadMessage()
// → "Command not allowed."
```

```bash
GOTOOLCHAIN=local go test ./http -run '^TestPOCCommandWebSocketUnboundedMessage$' -count=2 -v
```

The reporter's proof is bounded at 32 MiB and local-only; nothing in the handler imposes that bound.

## Impact

Any authenticated user can drive the server's heap with a single connection, regardless of whether they
hold `Perm.Execute` and regardless of whether `EnableExec` is set, because the rejection happens only
after the message has been read in full. Repeated or concurrent connections make the process
unresponsive or terminate it under ordinary container and host memory limits. Confidentiality and
integrity of stored files are not affected.

## Patches

None. The project is no longer maintained and no fix will ship.

## Workarounds

- Block `/api/command` at the reverse proxy unless the command feature is actually in use. The endpoint
  has no other purpose, and its route is registered even when `EnableExec` is false, so denying the path
  (or refusing WebSocket upgrades to it) removes the exposure without affecting anything else.
- Run File Browser under a memory limit with a restart policy, so an exhausted process is contained and
  recovered rather than taking the host with it.

## Out of scope

Command execution itself, the runner and the command allowlist. The reporter explicitly did not claim
execution or an allowlist bypass, and this advisory makes no claim about them either. That class is
known, unaddressed, and tracked separately in
[#5199](https://github.com/filebrowser/filebrowser/issues/5199).

## References

- GHSA-w5fm-68j4-fpc4 — DoS on the public login API; its fix introduced the `maxAuthBodySize` bound that
  the WebSocket path has no equivalent of.
- [#5199](https://github.com/filebrowser/filebrowser/issues/5199) — command execution, runner and hooks.
