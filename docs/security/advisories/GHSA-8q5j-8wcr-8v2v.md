# GHSA-8q5j-8wcr-8v2v / CVE-2026-82235 — Archive downloads hang forever on named pipes

- Severity: medium (CVSS 5.9)
- CWEs: CWE-400
- Affected: github.com/filebrowser/filebrowser/v2 <= 2.63.23
- Published: 2026-08-14T08:09:05Z
- URL: https://github.com/filebrowser/filebrowser/security/advisories/GHSA-8q5j-8wcr-8v2v

---

## Summary

The single-file download handler refuses named pipes, but the directory-archive walker does not:
archiving a directory that contains a FIFO calls `open(2)` on it, which blocks forever when no writer is
attached. The request goroutine and the client connection are pinned indefinitely — there is no timeout,
and context cancellation cannot interrupt a blocking `open(2)`. Repeated requests accumulate pinned
goroutines. The public download handler has the same gap, where it also applies to a share pointing
directly at a named pipe, so the hang is reachable by anonymous visitors holding a public share link.

Verified at v2.63.23, the final release.

## Details

The guard exists at the authenticated entry point only:

```go
// http/raw.go:94-97 (rawHandler)
if files.IsNamedPipe(file.Mode) {
	setContentDisposition(w, r, file)
	return 0, nil
}
```

It was added in [`586d198d`](https://github.com/filebrowser/filebrowser/commit/586d198d) ("fix: fix
hanging when reading a named pipe file", #1155). Two sibling paths reach `Open` without it.

`getFiles` appends every non-directory entry with a live `Open` callback and no entry-type check:

```go
// http/raw.go:136-142
archiveFiles = append(archiveFiles, archives.FileInfo{
	FileInfo:      info,
	NameInArchive: nameInArchive,
	Open: func() (fs.File, error) {
		return d.user.Fs.Open(path)
	},
})
```

`mholt/archives` `openAndCopyFile` then calls `file.Open()` unconditionally, and `open(2)` on a
writer-less FIFO blocks. `archiver.Archive` (`http/raw.go:212`) is passed the request context, but
cancellation cannot interrupt a blocking `open(2)` syscall, so the goroutine never returns even after the
client disconnects.

`publicDlHandler` calls `rawFileHandler` with no guard of its own, so a share pointing directly at a
named pipe hangs on the single-file path too:

```go
// http/public.go:131-138
var publicDlHandler = withHashFile(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	file := d.raw.(*files.FileInfo)
	if !file.IsDir {
		return rawFileHandler(w, r, file)
	}

	return rawDirHandler(w, r, d, file)
})
```

Creating such a share is not prevented: `sharePostHandler` only stats the path
(`http/share.go:159-168`), and `Stat` succeeds on a FIFO without opening it.

## PoC

```bash
BASE=http://127.0.0.1:8099
TOKEN=$(curl -s -X POST $BASE/api/login -H 'Content-Type: application/json' \
  -d '{"username":"editor","password":"…"}')

# place a FIFO inside the user's scope (host-side; a user with shell/CLI access to the
# storage tree, or any sync/mount tooling, can create one)
mkdir -p /srv/fb-root/shared/f && echo x > /srv/fb-root/shared/f/normal.txt
mkfifo /srv/fb-root/shared/f/pipe

# archive the directory — request never completes
time curl -s --max-time 15 -o /dev/null -w '%{http_code}\n' \
  "$BASE/api/raw/?files=%2Fshared%2Ff%2F&algo=zip" -H "X-Auth: $TOKEN"
# → curl exits 28 after exactly 15.0s, zero bytes (hang; replayed 2/2)

# anonymous variant: publicly share /shared/f and fetch
#   $BASE/api/public/dl/<hash>/   (archive of the shared dir) — same hang, no auth needed
```

With the FIFO removed, the same archive request returns 200 in ~2 ms. During the hang, concurrent
requests are still served — each hung request pins its own goroutine and connection.

## Impact

Any user with download permission — or an anonymous visitor holding a public share link — can pin one
server goroutine plus one connection per request, with no recovery short of restarting the process.
Scripted repetition accumulates pinned goroutines and connection slots into a denial-of-service
condition.

The named pipe has to be in the served tree already: File Browser's own API cannot create one, so this
requires host-side access to the storage — a shell, a mount, sync tooling, or the command-execution
feature.

## Patches

None. The project is no longer maintained and no fix will ship.

## Workarounds

- Do not serve trees that contain named pipes, sockets or device nodes. A dedicated data volume holding
  only regular files and directories removes the precondition entirely.
- A response timeout on the reverse proxy releases the client's connection, but note that it does **not**
  free the blocked server-side goroutine — only a restart does.

## Out of scope

- **Content disclosure through a FIFO with a live writer.** `openAndCopyFile` copies with
  `io.CopyN(w, fileReader, file.Size())`, and a FIFO's stat size is 0, so the entry is archived as zero
  bytes.
- **The preview endpoint.** `detectType` returns early with `Type: "blob"` for a named pipe
  (`files/file.go:222-226`), so `previewHandler` answers 501 before it can reach `rawFileHandler`.

## References

- [`586d198d`](https://github.com/filebrowser/filebrowser/commit/586d198d) — the named-pipe guard for
  single-file downloads (#1155).
