package fbhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filebrowser/filebrowser/v2/files"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/share"
	"github.com/filebrowser/filebrowser/v2/users"
)

// GHSA-3v48-283x-f2w4: the "download link" token shipped with a
// password-protected share must not be a password bypass. The stored random
// token alone must not fetch the file; only a token issued after the password
// was verified (bound to the current password hash and expiry) may do so.
func TestSharePasswordNotBypassedByDownloadToken(t *testing.T) {
	root := t.TempDir()
	userScope := filepath.Join(root, "user")
	if err := os.MkdirAll(userScope, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userScope, "file.txt"), []byte("secret content"), 0o600); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Share: true, Download: true}
	st := scopedUserStorage(t, userScope, perm, key)
	server := &settings.Server{Root: root}

	// Create a password-protected share through the real handler.
	body := `{"password":"ShareSecret123!","expires":"24","unit":"hours"}`
	req, _ := http.NewRequest(http.MethodPost, "/file.txt", strings.NewReader(body))
	req.Header.Set("X-Auth", signToken(t, perm, key))
	rec := httptest.NewRecorder()
	handle(sharePostHandler, "", st, server).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("share creation returned %d, body=%q", rec.Code, rec.Body.String())
	}
	var created struct {
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	link, err := st.Share.GetByHash(created.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if link.Token == "" || link.PasswordHash == "" {
		t.Fatalf("share was not password protected: token=%q hash=%q", link.Token, link.PasswordHash)
	}

	publicGet := func(path, query, password string) *httptest.ResponseRecorder {
		u := path
		if query != "" {
			u += "?" + query
		}
		r, _ := http.NewRequest(http.MethodGet, u, http.NoBody)
		if password != "" {
			r.Header.Set("X-SHARE-PASSWORD", url.QueryEscape(password))
		}
		w := httptest.NewRecorder()
		handle(publicDlHandler, "/api/public/dl/", st, server).ServeHTTP(w, r)
		return w
	}

	// The raw token stored with the share (the one shown as the "Download Link")
	// must not authenticate on its own.
	rec = publicGet("/api/public/dl/"+created.Hash+"/file.txt", "token="+url.QueryEscape(link.Token), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("VULNERABLE: stored download token bypassed the password (status %d, body %q)", rec.Code, rec.Body.String())
	}

	// Normal flow: supply the password to the share-info endpoint; the token it
	// returns is usable for the subsequent download.
	infoReq, _ := http.NewRequest(http.MethodGet, "/api/public/share/"+created.Hash+"/file.txt", http.NoBody)
	infoReq.Header.Set("X-SHARE-PASSWORD", url.QueryEscape("ShareSecret123!"))
	infoRec := httptest.NewRecorder()
	handle(publicShareHandler, "/api/public/share/", st, server).ServeHTTP(infoRec, infoReq)
	if infoRec.Code != http.StatusOK {
		t.Fatalf("password-protected share info returned %d, body=%q", infoRec.Code, infoRec.Body.String())
	}
	var info files.FileInfo
	if err := json.Unmarshal(infoRec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Token == "" {
		t.Fatal("share info did not return a post-verification download token")
	}

	rec = publicGet("/api/public/dl/"+created.Hash+"/file.txt", "token="+url.QueryEscape(info.Token), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("password-verified download token returned %d, body=%q; want 200", rec.Code, rec.Body.String())
	}

	// The issued token is bound to the password hash and expiry: changing either
	// invalidates it.
	link.Expire++
	if err := st.Share.Save(link); err != nil {
		t.Fatal(err)
	}
	rec = publicGet("/api/public/dl/"+created.Hash+"/file.txt", "token="+url.QueryEscape(info.Token), "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("VULNERABLE: token survived an expiry change (status %d)", rec.Code)
	}
	link.Expire--

	// Changing the password invalidates previously issued tokens; the new
	// password yields a fresh working token.
	newHash, status, err := getSharePasswordHash(share.CreateBody{Password: "BrandNewSecret456?"})
	if err != nil || status != 0 {
		t.Fatalf("failed to hash replacement password: %v %d", err, status)
	}
	link.PasswordHash = string(newHash)
	if err := st.Share.Save(link); err != nil {
		t.Fatal(err)
	}
	rec = publicGet("/api/public/dl/"+created.Hash+"/file.txt", "token="+url.QueryEscape(info.Token), "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("VULNERABLE: token survived a password change (status %d)", rec.Code)
	}
}
