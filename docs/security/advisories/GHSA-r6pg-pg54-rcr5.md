# GHSA-r6pg-pg54-rcr5 / CVE-2026-82236 — Deleting another user's shared file leaves their public share link behind

- Severity: low (CVSS 3.1)
- CWEs: CWE-459
- Affected: github.com/filebrowser/filebrowser/v2 >= 2.63.6, <= 2.63.23
- Published: 2026-08-14T08:09:07Z
- URL: https://github.com/filebrowser/filebrowser/security/advisories/GHSA-r6pg-pg54-rcr5

---

## Summary

When a file is deleted, File Browser cleans up the public share links that pointed at it — but only
those owned by the user performing the delete. If an administrator, or any other user whose scope
resolves the same path, deletes a file that someone else had publicly shared, the owner's share record
survives. When any file later appears at that path, the old unauthenticated link — original password and
expiry intact — serves the new, unrelated content.

Verified at v2.63.23, the final release. The owner filter on this path was introduced in v2.63.6 by the
fix for GHSA-5ww9-jg6q-38r7.

## Details

The delete handler passes the deleter's user ID into share cleanup:

```go
// http/resource.go:110 (resourceDeleteHandler)
err = d.store.Share.DeleteWithPathPrefix(file.Path, d.user.ID)
```

The Bolt backend then skips every share that is not owned by that user:

```go
// storage/bolt/share.go:93-96 (DeleteWithPathPrefix)
for _, link := range links {
	if link.UserID != userID {
		continue
	}
```

That filter was added in [`0231b7eb`](https://github.com/filebrowser/filebrowser/commit/0231b7eb) to stop
one user from deleting another user's share links through the share-management API. It applies to this
filesystem-event cleanup path as well, so the cleanup now misses exactly the shares that the delete has
invalidated. `withHashFile` resolves `link.Path` lazily on each request (`http/public.go:41-53`), so the
surviving link 404s while the path is absent and serves whatever file appears there next.

## PoC

Two users, `editor` and `admin`, both scoped to `/`.

```bash
BASE=http://127.0.0.1:8099
ED=$(curl -s -X POST $BASE/api/login -H 'Content-Type: application/json' \
  -d '{"username":"editor","password":"…"}')
AD=$(curl -s -X POST $BASE/api/login -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"…"}')

# 1. editor creates and publicly shares a file
curl -s -X POST "$BASE/api/resources/x.txt" -H "X-Auth: $ED" --data 'OLD-CONTENT'
HASH=$(curl -s -X POST "$BASE/api/share/x.txt" -H "X-Auth: $ED" -d '{}' | jq -r .hash)
curl -s "$BASE/api/public/dl/$HASH/x.txt"          # → OLD-CONTENT (unauthenticated)

# 2. ADMIN deletes the file
curl -s -o /dev/null -w '%{http_code}\n' -X DELETE "$BASE/api/resources/x.txt" \
  -H "X-Auth: $AD"                                 # → 204

# 3. editor's share record survived the admin's delete
curl -s "$BASE/api/shares" -H "X-Auth: $ED"        # → still lists {"path":"/x.txt",…}

# 4. editor recreates the file with new content
curl -s -X POST "$BASE/api/resources/x.txt" -H "X-Auth: $ED" --data 'NEW-CONTENT'

# 5. the old link serves the NEW file, unauthenticated
curl -s "$BASE/api/public/dl/$HASH/x.txt"          # → NEW-CONTENT   (replayed 2/2)
```

When `editor` deletes her **own** shared file, the share is removed and the recreated file stays private
(404) — confirming the cleanup works only for same-user deletes.

## Impact

Any workflow where a privileged user deletes another user's shared file — admin cleanup, a shared scope —
silently leaves the public link alive. The link later exposes whatever new file appears at that path,
unauthenticated, with no indication to the owner that their link is still live.

## Patches

None. The project is no longer maintained and no fix will ship.

## Workarounds

- Administrators can audit every user's links with `GET /api/shares` and revoke any of them with
  `DELETE /api/share/{hash}`. Do that after deleting a path that belongs to another user.
- Give shares an expiry and a password, so a resurrected link is time-bounded and not anonymously usable.
- Avoid granting several accounts delete permission over one identical scope.

## Out of scope

Share paths are stored relative to the owner's scope (`Path: r.URL.Path` in `sharePostHandler`, resolved
against the owner's filesystem in `withHashFile`). The gap is therefore only reachable when the deleter
and the share owner resolve the *same* path string — in practice, when they hold the same scope. Where
scopes overlap under different roots, the stored path and the deleted path differ, and the prefix query
would not match the other user's share with or without the owner filter.

For the same reason, this advisory does not endorse the reported remediation of dropping the owner filter
outright: that would let one user's delete destroy an unrelated user's share whose scope-relative path
merely happens to collide, re-opening GHSA-5ww9-jg6q-38r7. A correct fix would have to resolve each
candidate share's path through its own owner's scope before deciding.

## References

- GHSA-5ww9-jg6q-38r7 — cross-user share-link deletion; its fix introduced the owner filter on this path.
- GHSA-m8v4-4w34-rrvf — the rename path performs no share cleanup at all.
- GHSA-pp88-jhwj-5qh5 — stale share left by a trailing-slash delete (the same impact class).
