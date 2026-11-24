package fbhttp

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filebrowser/filebrowser/v2/diskcache"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/users"
)

type errReader struct {
	err error
}

func (r errReader) Read([]byte) (int, error) {
	return 0, r.err
}

func TestUploadToDirectoryRejected(t *testing.T) {
	root := t.TempDir()
	userScope := filepath.Join(root, "user")
	targetDir := filepath.Join(userScope, "team")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(targetDir, "plan.txt")
	if err := os.WriteFile(child, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true, Modify: true}
	st := scopedUserStorage(t, userScope, perm, key)
	signed := signToken(t, perm, key)

	req, _ := http.NewRequest(http.MethodPost, "/team?override=true", strings.NewReader("x"))
	req.Header.Set("X-Auth", signed)
	rec := httptest.NewRecorder()
	handle(resourcePostHandler(diskcache.NewNoOp()), "", st, &settings.Server{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /team?override=true = %d, body=%q; want 400", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(child); err != nil {
		t.Fatalf("existing child deleted: %v", err)
	}
	if got, err := os.ReadFile(child); err != nil || string(got) != "data" {
		t.Fatalf("existing child changed: %q err=%v", got, err)
	}
}

func TestFailedUploadDoesNotDeleteExistingTree(t *testing.T) {
	root := t.TempDir()
	userScope := filepath.Join(root, "user")
	targetDir := filepath.Join(userScope, "team")
	if err := os.MkdirAll(filepath.Join(targetDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(targetDir, "plan.txt"):          "data",
		filepath.Join(targetDir, "sub", "secret.txt"): "secret",
	}
	for name, content := range files {
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true, Modify: true}
	st := scopedUserStorage(t, userScope, perm, key)
	signed := signToken(t, perm, key)

	req, _ := http.NewRequest(http.MethodPost, "/team?override=true", strings.NewReader("x"))
	req.Header.Set("X-Auth", signed)
	rec := httptest.NewRecorder()
	handle(resourcePostHandler(diskcache.NewNoOp()), "", st, &settings.Server{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /team?override=true = %d, body=%q; want 400", rec.Code, rec.Body.String())
	}
	for name, content := range files {
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("file %s missing after failed upload: %v", name, err)
		}
		if string(got) != content {
			t.Fatalf("file %s changed: got %q want %q", name, got, content)
		}
	}
}

func TestUploadCleanupDoesNotBypassDeletePerm(t *testing.T) {
	root := t.TempDir()
	userScope := filepath.Join(root, "user")
	if err := os.MkdirAll(userScope, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(userScope, "file.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true, Modify: true, Delete: false}
	st := scopedUserStorage(t, userScope, perm, key)
	signed := signToken(t, perm, key)

	req, _ := http.NewRequest(http.MethodPost, "/file.txt?override=true", io.NopCloser(errReader{err: errors.New("boom")}))
	req.Header.Set("X-Auth", signed)
	rec := httptest.NewRecorder()
	handle(resourcePostHandler(diskcache.NewNoOp()), "", st, &settings.Server{}).ServeHTTP(rec, req)

	if rec.Code < 400 {
		t.Fatalf("expected failure status, got %d", rec.Code)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("existing file removed by failed upload cleanup: %v", err)
	}
	if string(got) != "keep" {
		t.Fatalf("existing file changed by failed upload: %q", got)
	}
}

func TestUploadThroughSymlinkCleanup(t *testing.T) {
	root := t.TempDir()
	userScope := filepath.Join(root, "user")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{userScope, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(userScope, "link")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true}
	st := scopedUserStorage(t, userScope, perm, key)
	signed := signToken(t, perm, key)

	req, _ := http.NewRequest(http.MethodPost, "/link/victim.txt", strings.NewReader("x"))
	req.Header.Set("X-Auth", signed)
	rec := httptest.NewRecorder()
	handle(resourcePostHandler(diskcache.NewNoOp()), "", st, &settings.Server{}).ServeHTTP(rec, req)

	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("VULNERABLE: out-of-scope victim deleted by upload cleanup (status=%d): %v", rec.Code, err)
	}
}

func TestDanglingSymlinkWriteStaysInScope(t *testing.T) {
	root := t.TempDir()
	userScope := filepath.Join(root, "user")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{userScope, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	outsideTarget := filepath.Join(outside, "created.txt")
	if err := os.Symlink(outsideTarget, filepath.Join(userScope, "evil")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true, Modify: true}
	st := scopedUserStorage(t, userScope, perm, key)
	signed := signToken(t, perm, key)

	req, _ := http.NewRequest(http.MethodPost, "/evil?override=true", strings.NewReader("http-outside"))
	req.Header.Set("X-Auth", signed)
	rec := httptest.NewRecorder()
	handle(resourcePostHandler(diskcache.NewNoOp()), "", st, &settings.Server{}).ServeHTTP(rec, req)

	if _, err := os.Stat(outsideTarget); err == nil {
		t.Fatalf("VULNERABLE: out-of-scope file created via dangling symlink (status=%d)", rec.Code)
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%q", rec.Code, rec.Body.String())
	}
}

func TestPutFailureDoesNotTruncateExistingFile(t *testing.T) {
	root := t.TempDir()
	userScope := filepath.Join(root, "user")
	if err := os.MkdirAll(userScope, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(userScope, "file.txt")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true, Modify: true}
	st := scopedUserStorage(t, userScope, perm, key)
	signed := signToken(t, perm, key)

	req, _ := http.NewRequest(http.MethodPut, "/file.txt", io.NopCloser(errReader{err: errors.New("boom")}))
	req.Header.Set("X-Auth", signed)
	rec := httptest.NewRecorder()
	handle(resourcePutHandler, "", st, &settings.Server{}).ServeHTTP(rec, req)

	if rec.Code < 400 {
		t.Fatalf("expected failure status, got %d", rec.Code)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("existing file removed by failed PUT: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("existing file truncated/changed by failed PUT: %q", got)
	}
}

// Overwriting a file must not silently reset its mode: the temp-file + rename
// flow replaces the inode, so the temp takes the destination's existing mode
// (a user editing an executable script at 0755 through the web editor must not
// lose the execute bit). New files keep the configured fileMode.
func TestOverwritePreservesFileMode(t *testing.T) {
	root := t.TempDir()
	userScope := filepath.Join(root, "user")
	if err := os.MkdirAll(userScope, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(userScope, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho v1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Modify: true, Download: true}
	st := scopedUserStorage(t, userScope, perm, key)
	signed := signToken(t, perm, key)

	req, _ := http.NewRequest(http.MethodPut, "/run.sh", strings.NewReader("#!/bin/sh\necho v2\n"))
	req.Header.Set("X-Auth", signed)
	rec := httptest.NewRecorder()
	handle(resourcePutHandler, "", st, &settings.Server{}).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, body=%q; want 200", rec.Code, rec.Body.String())
	}
	if got, err := os.ReadFile(script); err != nil || string(got) != "#!/bin/sh\necho v2\n" {
		t.Fatalf("content = %q, err = %v", got, err)
	}
	if fi, err := os.Stat(script); err != nil {
		t.Fatal(err)
	} else if mode := fi.Mode().Perm(); mode != 0o755 {
		t.Fatalf("VULNERABLE to silent permission loss: mode = %o, want 755", mode)
	}
}
