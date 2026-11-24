package fbhttp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/asdine/storm/v3"
	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/files"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/storage"
	"github.com/filebrowser/filebrowser/v2/storage/bolt"
	"github.com/filebrowser/filebrowser/v2/users"
)

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
