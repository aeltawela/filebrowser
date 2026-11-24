# GHSA-448h-jr2h-3vhp / CVE-2026-90928 — Subtitle conversion reads whole files into memory, allowing memory exhaustion

- Severity: high
- CWEs: CWE-400
- Affected: github.com/filebrowser/filebrowser/v2 <= 2.63.23
- Published: 2026-08-31T06:27:04Z
- URL: https://github.com/filebrowser/filebrowser/security/advisories/GHSA-448h-jr2h-3vhp

---

## Summary

The subtitle endpoint converts `.srt`, `.ass` and `.ssa` files to WebVTT by loading them into memory in full, with no size limit anywhere on the path. An authenticated user can request conversion of a large subtitle file and make the server allocate several times its size; concurrent requests exhaust memory and terminate the process. The 10 MB cap that bounds the text-content read path does not apply here, because the subtitle handler never asks for file content and so never reaches that check. Reported against v2.63.23 and verified at v2.63.23.

## Details

`subtitleHandler` in `http/subtitle.go` resolves the file with `files.NewFileInfo` leaving `Content` unset. `detectType` therefore takes no content-reading branch, and its size check —

```go
	case (strings.HasPrefix(mimetype, "text") || !isBinary(buffer)) && i.Size <= 10*1024*1024: // 10 MB
```

— never constrains what follows. `subtitleFileHandler` then opens the file itself and reads all of it:

```go
	if strings.HasSuffix(file.Name, ".srt") {
		content, readErr := io.ReadAll(fd)
		if readErr != nil {
			return http.StatusInternalServerError, readErr
		}
		sub, err = astisub.ReadFromSRT(bytes.NewReader(normalizeSRTLineBreaks(content)))
	} else if strings.HasSuffix(file.Name, ".ass") || strings.HasSuffix(file.Name, ".ssa") {
		sub, err = astisub.ReadFromSSA(fd)
	}
```

For an N-byte `.srt` the handler holds the `io.ReadAll` buffer, a second full copy produced by `normalizeSRTLineBreaks` — `regexp.ReplaceAll` returns a new slice — and the parsed `astisub.Subtitles` structure, before `WriteToWebVTT` renders the result into a third buffer. Peak usage is a multiple of the file size. The `.ass`/`.ssa` branch streams the descriptor rather than buffering it, but `astisub.ReadFromSSA` still builds an in-memory structure proportional to the input, so it is unbounded too.

Two distinct phases are worth separating. The unbounded parse has existed since the subtitle feature shipped in v2.0.0. The `io.ReadAll` call and the extra full copy were added in v2.63.17, by the fix preserving SRT line breaks, which raised the multiplier on the `.srt` path.

The endpoint is authenticated only. `/api/subtitle` is registered on the authenticated router in `http/http.go`, and no public-share route reaches `subtitleFileHandler` — `publicDlHandler` serves through `rawFileHandler`, which streams.

## PoC

Preconditions: an authenticated account with `Perm.Download`, and a large subtitle file within its scope. `Perm.Create` lets the attacker supply one; otherwise any sufficiently large existing `.srt` will do.

1. Authenticate against `POST /api/login` and keep the token.
2. Upload a large `.srt`:

   ```http
   POST /api/resources/dos.srt?override=true HTTP/1.1
   Host: TARGET
   X-Auth: <token>
   Content-Type: application/octet-stream

   <a few tens of MB of SRT text>
   ```

3. Request the conversion:

   ```http
   GET /api/subtitle/dos.srt HTTP/1.1
   Host: TARGET
   X-Auth: <token>
   ```

The server reads the file in full and allocates a multiple of its size. Issuing the request repeatedly and concurrently drives resident memory up until the process is killed.

## Impact

An authenticated user with download permission can exhaust the server's memory and terminate it, denying service to every user of the instance. In containers with a memory limit the process is killed promptly; without one it can starve the host. Confidentiality and integrity are unaffected.

## Patches

None. The project is no longer maintained and no fix will ship.

## Workarounds

- Withhold `Perm.Create` from untrusted accounts so they cannot place a large subtitle file, and check existing scopes for oversized `.srt`, `.ass` and `.ssa` files. Download permission alone is not enough to exploit this if no such file exists.
- Run File Browser with a memory limit and automatic restart, so a successful attack costs an instance restart rather than the host.
- Where a reverse proxy fronts File Browser, rate-limit `GET /api/subtitle/` per account.

## Out of scope

The report presents this as a bypass of the CVE-2025-53893 patch. It is not, and the advisory does not describe it that way:

- The 10 MB cap in `files/file.go` was not introduced by a fix for CVE-2025-53893. It arrived in v2.12.0, released in 2021, several years before that report, and GHSA-7xqm-7738-642x shipped with no patched version at all.
- The subtitle path was never covered by that cap, so there is no incomplete fix here. This is a separate unbounded sink on a different endpoint, and it is treated as its own defect.

## References

- Earlier unbounded-read report on the text-content path: GHSA-7xqm-7738-642x
- Size check that bounds the text-content path and not this one: `files/file.go`, `detectType`
