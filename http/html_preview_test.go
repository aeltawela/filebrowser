package fbhttp

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asdine/storm/v3"
	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/storage"
	"github.com/filebrowser/filebrowser/v2/storage/bolt"
	"github.com/filebrowser/filebrowser/v2/users"
)

func TestHTMLPreviewHandlerRequiresSetting(t *testing.T) {
	st, token := setupHTMLPreviewTest(t, false)

	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req.Header.Set("X-Auth", token)
	rec := httptest.NewRecorder()

	handle(htmlPreviewHandler, "", st, &settings.Server{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestHTMLPreviewHandlerServesSandboxedHTML(t *testing.T) {
	st, token := setupHTMLPreviewTest(t, true)

	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	req.Header.Set("X-Auth", token)
	rec := httptest.NewRecorder()

	handle(htmlPreviewHandler, "", st, &settings.Server{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Body.String(); got != "<script>window.previewLoaded = true;</script>" {
		t.Fatalf("body = %q", got)
	}

	csp := rec.Header().Get("Content-Security-Policy")
	for _, token := range []string{"sandbox", "allow-scripts", "allow-same-origin", "allow-forms", "allow-popups"} {
		if !strings.Contains(csp, token) {
			t.Fatalf("Content-Security-Policy = %q, missing %q", csp, token)
		}
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", got)
	}
}

func setupHTMLPreviewTest(t *testing.T, enabled bool) (*storage.Storage, string) {
	t.Helper()

	key := []byte("test-signing-key")
	db, err := storm.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	st, err := bolt.NewStorage(db)
	if err != nil {
		t.Fatalf("failed to get storage: %v", err)
	}

	perm := users.Permissions{Download: true}
	if err := st.Users.Save(&users.User{Username: "u", Password: "pw", Perm: perm}); err != nil {
		t.Fatalf("failed to save user: %v", err)
	}
	if err := st.Settings.Save(&settings.Settings{Key: key, HTMLPreview: enabled}); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/index.html", []byte("<script>window.previewLoaded = true;</script>"), 0o600); err != nil {
		t.Fatalf("failed to write preview file: %v", err)
	}
	st.Users = &customFSUser{
		Store: st.Users,
		fs:    fs,
	}

	return st, signToken(t, perm, key)
}
