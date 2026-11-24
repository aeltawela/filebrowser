package fbhttp

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/asdine/storm/v3"
	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/files"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/share"
	"github.com/filebrowser/filebrowser/v2/storage"
	"github.com/filebrowser/filebrowser/v2/storage/bolt"
	"github.com/filebrowser/filebrowser/v2/users"
)

// GHSA-448h-jr2h-3vhp: subtitle conversion must reject oversized inputs with
// 413 instead of reading them fully into memory. Normal files still convert.
func TestSubtitleSizeLimit(t *testing.T) {
	// A small valid SRT must keep converting (SRT <br> fix intact).
	t.Run("small SRT converts", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		const path = "/sample.srt"
		const content = "1\n00:00:01,000 --> 00:00:02,000\nFirst<br>Second\n\n"
		if err := afero.WriteFile(fs, path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		info, _ := fs.Stat(path)
		file := &files.FileInfo{Fs: fs, Path: path, Name: "sample.srt", Size: info.Size(), ModTime: info.ModTime()}
		req := httptest.NewRequest(http.MethodGet, "/api/subtitle/sample.srt?inline=true", http.NoBody)
		rec := httptest.NewRecorder()
		if status, err := subtitleFileHandler(rec, req, file); err != nil || status != 0 {
			t.Fatalf("small SRT: status=%d err=%v, want conversion to succeed", status, err)
		}
		if !strings.Contains(rec.Body.String(), "First\nSecond") {
			t.Fatalf("small SRT: <br> fix lost, body=%q", rec.Body.String())
		}
	})

	// Oversized inputs must be rejected with 413 before parsing.
	for _, name := range []string{"big.srt", "big.ass"} {
		t.Run("oversized "+name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			const path = "/big"
			var content []byte
			if strings.HasSuffix(name, ".srt") {
				content = []byte(strings.Repeat("1\n00:00:01,000 --> 00:00:02,000\nhello\n\n", 1<<17))
			} else {
				content = []byte("[Script Info]\nTitle: big\n\n[V4+ Styles]\nFormat: Name\nStyle: Default\n\n[Events]\nFormat: Layer, Start, End, Style, Text\n" +
					strings.Repeat("Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,hello\n", 1<<17))
			}
			if int64(len(content)) <= SubtitleMaxBytes {
				t.Fatalf("test setup: content %d bytes does not exceed limit %d", len(content), SubtitleMaxBytes)
			}
			if err := afero.WriteFile(fs, path, content, 0o644); err != nil {
				t.Fatal(err)
			}
			info, _ := fs.Stat(path)
			file := &files.FileInfo{Fs: fs, Path: path, Name: name, Size: info.Size(), ModTime: info.ModTime()}
			req := httptest.NewRequest(http.MethodGet, "/api/subtitle/"+name+"?inline=true", http.NoBody)
			rec := httptest.NewRecorder()
			status, _ := subtitleFileHandler(rec, req, file)
			if status != http.StatusRequestEntityTooLarge {
				t.Fatalf("VULNERABLE: oversized %s: status=%d, want 413", name, status)
			}
		})
	}
}

// The WebVTT output cap is the second half of the GHSA-448h-jr2h-3vhp bound:
// conversion output that expands without bound must be rejected instead of
// filling memory, even when the input itself passed the input cap.
func TestSubtitleOutputCap(t *testing.T) {
	var buf bytes.Buffer
	c := &cappedWriter{w: &buf, limit: 8}
	if _, err := c.Write([]byte("12345678")); err != nil {
		t.Fatalf("write within the cap failed: %v", err)
	}
	if _, err := c.Write([]byte("x")); err == nil {
		t.Fatal("VULNERABLE: write past the output cap was accepted")
	}
}

// w3MemStorage mirrors scopedUserStorage but serves a caller-supplied afero
// filesystem, so tests can use a fast in-memory tree.
func w3MemStorage(t *testing.T, fs afero.Fs, perm users.Permissions, key []byte) *storage.Storage {
	t.Helper()
	db, err := storm.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	st, err := bolt.NewStorage(db)
	if err != nil {
		t.Fatalf("failed to get storage: %v", err)
	}
	if err := st.Users.Save(&users.User{Username: "u", Password: "pw", Perm: perm}); err != nil {
		t.Fatalf("failed to save user: %v", err)
	}
	if err := st.Settings.Save(&settings.Settings{Key: key}); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}
	st.Users = &customFSUser{Store: st.Users, fs: fs}
	return st
}

// w3ServeRaw runs h in a goroutine and fails the test if it does not return
// within timeout: archive/FIFO hangs must be caught, not waited out forever.
func w3ServeRaw(t *testing.T, h http.Handler, req *http.Request, timeout time.Duration) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rec, req)
	}()
	select {
	case <-done:
		return rec
	case <-time.After(timeout):
		t.Fatalf("handler did not return within %s (hang?)", timeout)
		return nil
	}
}

// mkfifo creates a POSIX named pipe, skipping cleanly where unsupported.
func mkfifo(path string) error {
	return syscall.Mkfifo(path, 0o600)
}

// GHSA-8q5j-8wcr-8v2v: archiving a directory containing a FIFO must complete
// promptly and omit the FIFO instead of blocking forever in open(2).
func TestArchiveSkipsNamedPipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipes require POSIX mkfifo")
	}
	root := t.TempDir()
	scope := filepath.Join(root, "user")
	dir := filepath.Join(scope, "d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "normal.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mkfifo(filepath.Join(dir, "pipe")); err != nil {
		t.Skipf("cannot create fifo: %v", err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Download: true}
	st := scopedUserStorage(t, scope, perm, key)
	signed := signToken(t, perm, key)

	req, _ := http.NewRequest(http.MethodGet, "/d?algo=zip", http.NoBody)
	req.Header.Set("X-Auth", signed)
	rec := w3ServeRaw(t, handle(rawHandler, "", st, &settings.Server{}), req, 15*time.Second)

	result := rec.Result()
	defer result.Body.Close()
	body, _ := io.ReadAll(result.Body)
	if result.StatusCode != http.StatusOK {
		t.Fatalf("archive with FIFO: status=%d body=%q, want 200", result.StatusCode, body)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("failed to read zip: %v", err)
	}
	for _, f := range zr.File {
		if strings.Contains(f.Name, "pipe") {
			t.Errorf("VULNERABLE: archive includes named pipe entry %q", f.Name)
		}
	}
}

// GHSA-8q5j-8wcr-8v2v: a public share pointing directly at a FIFO must answer
// promptly instead of hanging the anonymous visitor's connection.
func TestPublicShareNamedPipeDoesNotHang(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipes require POSIX mkfifo")
	}
	root := t.TempDir()
	scope := filepath.Join(root, "user")
	if err := os.MkdirAll(scope, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := mkfifo(filepath.Join(scope, "pipe")); err != nil {
		t.Skipf("cannot create fifo: %v", err)
	}

	db, err := storm.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := bolt.NewStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Share.Save(&share.Link{Hash: "h", UserID: 1, Path: "/pipe"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Users.Save(&users.User{
		Username: "username", Password: "pw",
		Perm: users.Permissions{Share: true, Download: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Settings.Save(&settings.Settings{Key: []byte("key")}); err != nil {
		t.Fatal(err)
	}
	st.Users = &customFSUser{Store: st.Users, fs: files.NewScopedFs(afero.NewOsFs(), scope)}

	req := newHTTPRequest(t, func(r *http.Request) { r.URL.Path = "h" })
	rec := w3ServeRaw(t, handle(publicDlHandler, "", st, &settings.Server{}), req, 15*time.Second)

	result := rec.Result()
	defer result.Body.Close()
	body, _ := io.ReadAll(result.Body)
	if result.StatusCode != http.StatusForbidden {
		t.Errorf("FIFO share returned %d, want 403", result.StatusCode)
	}
	if string(body) != "403 Forbidden\n" {
		t.Errorf("FIFO share served %q, want only the 403 status text", body)
	}
}

// GHSA-8q5j-8wcr-8v2v: archive entry-count and total-byte limits return 413.
func TestArchiveLimits(t *testing.T) {
	t.Run("entry count", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		if err := fs.MkdirAll("/d", 0o755); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < ArchiveMaxEntries+1; i++ {
			p := filepath.Join("/d", "f"+strconv.Itoa(i))
			if err := afero.WriteFile(fs, p, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		key := []byte("test-signing-key")
		perm := users.Permissions{Download: true}
		st := w3MemStorage(t, fs, perm, key)
		signed := signToken(t, perm, key)

		req, _ := http.NewRequest(http.MethodGet, "/d?algo=zip", http.NoBody)
		req.Header.Set("X-Auth", signed)
		rec := httptest.NewRecorder()
		handle(rawHandler, "", st, &settings.Server{}).ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("VULNERABLE: %d entries: status=%d, want 413", ArchiveMaxEntries+1, rec.Code)
		}
	})

	t.Run("total bytes", func(t *testing.T) {
		root := t.TempDir()
		scope := filepath.Join(root, "user")
		if err := os.MkdirAll(filepath.Join(scope, "d"), 0o755); err != nil {
			t.Fatal(err)
		}
		// Sparse file: huge logical size, no disk or memory cost. The fixed
		// handler rejects it during the walk, before any byte is read.
		big := filepath.Join(scope, "d", "big.bin")
		f, err := os.Create(big)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(ArchiveMaxTotalBytes + 1); err != nil {
			f.Close()
			t.Skipf("cannot create sparse file: %v", err)
		}
		f.Close()

		key := []byte("test-signing-key")
		perm := users.Permissions{Download: true}
		st := scopedUserStorage(t, scope, perm, key)
		signed := signToken(t, perm, key)

		req, _ := http.NewRequest(http.MethodGet, "/d?algo=zip", http.NoBody)
		req.Header.Set("X-Auth", signed)
		rec := w3ServeRaw(t, handle(rawHandler, "", st, &settings.Server{}), req, 20*time.Second)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("VULNERABLE: oversized archive: status=%d, want 413", rec.Code)
		}
	})
}

// Post-walk growth must charge only the byte budget: the entry was already
// counted at walk time, and counting it again could spuriously reject a
// legitimate archive near the entry limit.
func TestArchiveBudgetAddBytes(t *testing.T) {
	b := &archiveBudget{}
	if err := b.add(false, 10); err != nil {
		t.Fatal(err)
	}
	if err := b.addBytes(5); err != nil {
		t.Fatal(err)
	}
	if b.entries != 1 || b.bytes != 15 {
		t.Fatalf("entries=%d bytes=%d, want 1 and 15", b.entries, b.bytes)
	}
	b.bytes = ArchiveMaxTotalBytes
	if err := b.addBytes(1); err == nil {
		t.Fatal("expected ErrArchiveTooLarge once the byte bound is exceeded")
	}
}

// countingCache decorates an UploadCache and records completion calls, so
// tests can prove the upload hook path fires exactly once.
type countingCache struct {
	UploadCache
	mu        sync.Mutex
	completes map[string]int
}

func newCountingCache() *countingCache {
	c := &countingCache{completes: map[string]int{}}
	c.UploadCache = newMemoryUploadCache()
	return c
}

func (c *countingCache) Complete(path string) {
	c.mu.Lock()
	c.completes[path]++
	c.mu.Unlock()
	c.UploadCache.Complete(path)
}

func (c *countingCache) count(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	// ScopedFs RealPath may resolve host symlinks (e.g. TMPDIR), so match by
	// trailing virtual path instead of exact string equality.
	n := 0
	for k, v := range c.completes {
		if strings.HasSuffix(k, path) {
			n += v
		}
	}
	return n
}

// w3TusFixture is newTusTestFixture with a caller-supplied cache.
type w3TusFixture struct {
	srv    *httptest.Server
	client *http.Client
	token  string
	scope  string
	cache  *countingCache
}

func newW3TusFixture(t *testing.T, cache *countingCache) *w3TusFixture {
	t.Helper()
	root := t.TempDir()
	scope := filepath.Join(root, "user")
	if err := os.MkdirAll(scope, 0o755); err != nil {
		t.Fatal(err)
	}
	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true, Modify: true}
	st := scopedUserStorage(t, scope, perm, key)
	t.Cleanup(cache.Close)
	srv := newTusTestServer(t, st, cache)
	return &w3TusFixture{srv: srv, client: srv.Client(), token: signToken(t, perm, key), scope: scope, cache: cache}
}

// GHSA-4r8p-gqj2-mwgm: concurrent PATCHes at the same offset must not write
// past Upload-Length, and completion must happen exactly once.
//
// The bodies are delivered through pipes that stay empty until every handler
// has arrived: no handler can observe another's bytes during the stat check,
// so on the unfixed code every PATCH passes the offset check and appends.
func TestTUSConcurrentPatchesRespectLength(t *testing.T) {
	const length = 1 << 20
	const racers = 8

	cache := newCountingCache()
	f := newW3TusFixture(t, cache)

	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/tus/race.bin?override=false", http.NoBody)
	req.Header.Set("X-Auth", f.token)
	req.Header.Set("Upload-Length", strconv.Itoa(length))
	res, err := f.client.Do(req)
	if err != nil {
		t.Fatalf("POST create: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST create: status %d", res.StatusCode)
	}
	uploadURL := f.srv.URL + res.Header.Get("Location")

	payload := bytes.Repeat([]byte{0x61}, length)
	statuses := make([]int, racers)
	var wg sync.WaitGroup
	pipes := make([]*io.PipeWriter, racers)
	for i := 0; i < racers; i++ {
		pr, pw := io.Pipe()
		pipes[i] = pw
		r, err := http.NewRequest(http.MethodPatch, uploadURL, pr)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("X-Auth", f.token)
		r.Header.Set("Content-Type", "application/offset+octet-stream")
		r.Header.Set("Upload-Offset", "0")
		r.ContentLength = length
		wg.Add(1)
		go func(i int, r *http.Request) {
			defer wg.Done()
			resp, err := f.client.Do(r)
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			statuses[i] = resp.StatusCode
		}(i, r)
	}

	// Every handler is now started and blocked waiting for body bytes, so all
	// of them stat size 0. Release the bodies in small trickles to keep the
	// writes overlapped even on a slow scheduler.
	time.Sleep(300 * time.Millisecond)
	var ww sync.WaitGroup
	for _, pw := range pipes {
		ww.Add(1)
		go func(pw *io.PipeWriter) {
			defer ww.Done()
			defer pw.Close()
			for off := 0; off < len(payload); off += 64 << 10 {
				end := min(off+(64<<10), len(payload))
				if _, err := pw.Write(payload[off:end]); err != nil {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}(pw)
	}
	ww.Wait()
	wg.Wait()

	winners := 0
	for _, s := range statuses {
		if s == http.StatusNoContent {
			winners++
		}
	}
	fi, err := os.Stat(filepath.Join(f.scope, "race.bin"))
	if err != nil {
		t.Fatalf("stat uploaded file: %v", err)
	}
	if fi.Size() != length {
		t.Fatalf("VULNERABLE: concurrent PATCH wrote %d bytes for Upload-Length %d (winners=%d)", fi.Size(), length, winners)
	}
	if winners != 1 {
		t.Fatalf("VULNERABLE: %d PATCHes succeeded, want exactly 1", winners)
	}
	if got := cache.count("/race.bin"); got != 1 {
		t.Fatalf("VULNERABLE: completion fired %d times, want exactly 1", got)
	}
}

// GHSA-ffx7-75gc-jg7c: negative Upload-Length is already rejected; negative
// Upload-Offset must be rejected too. Neither may complete an upload.
func TestTUSRejectsNegativeLengthAndOffset(t *testing.T) {
	cache := newCountingCache()
	f := newW3TusFixture(t, cache)

	// Negative length: POST itself must fail.
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/tus/neg.bin?override=false", http.NoBody)
	req.Header.Set("X-Auth", f.token)
	req.Header.Set("Upload-Length", "-1")
	res, err := f.client.Do(req)
	if err != nil {
		t.Fatalf("POST negative length: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST Upload-Length -1: status=%d body=%q, want 400", res.StatusCode, body)
	}

	// Negative offset: PATCH must fail with 400 (not 409, not success).
	req, _ = http.NewRequest(http.MethodPost, f.srv.URL+"/api/tus/off.bin?override=false", http.NoBody)
	req.Header.Set("X-Auth", f.token)
	req.Header.Set("Upload-Length", "5")
	res, err = f.client.Do(req)
	if err != nil {
		t.Fatalf("POST create: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST create: status %d", res.StatusCode)
	}
	uploadURL := f.srv.URL + res.Header.Get("Location")

	req, _ = http.NewRequest(http.MethodPatch, uploadURL, strings.NewReader("hello"))
	req.Header.Set("X-Auth", f.token)
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", "-1")
	res, err = f.client.Do(req)
	if err != nil {
		t.Fatalf("PATCH negative offset: %v", err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("VULNERABLE: PATCH Upload-Offset -1: status=%d body=%q, want 400", res.StatusCode, body)
	}
	if fi, err := os.Stat(filepath.Join(f.scope, "off.bin")); err == nil && fi.Size() != 0 {
		t.Fatalf("negative-offset PATCH wrote %d bytes", fi.Size())
	}
	if got := cache.count("/off.bin"); got != 0 {
		t.Fatalf("negative-offset PATCH completed the upload %d times", got)
	}
}
