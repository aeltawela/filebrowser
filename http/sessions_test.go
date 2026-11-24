package fbhttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	fberrors "github.com/filebrowser/filebrowser/v2/errors"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/storage"
	"github.com/filebrowser/filebrowser/v2/users"
	"github.com/gorilla/mux"
)

// newSessionStore builds a store with a single user (ID 1, name "u") and a
// signing key, reusing the filesystem-injecting helper from the other http
// tests so that these tests exercise the real handler path.
func newSessionStore(t *testing.T, perm users.Permissions, key []byte) (*storage.Storage, *settings.Server) {
	t.Helper()
	root := t.TempDir()
	return scopedUserStorage(t, root, perm, key), &settings.Server{Root: root}
}

func protectedSessionHandler(st *storage.Storage, server *settings.Server) http.Handler {
	return handle(withUser(func(w http.ResponseWriter, _ *http.Request, _ *data) (int, error) {
		_, err := w.Write([]byte("ok"))
		return 0, err
	}), "", st, server)
}

func putUserRequest(token, body string) *http.Request {
	req, _ := http.NewRequest(http.MethodPut, "/1", strings.NewReader(body))
	req.Header.Set("X-Auth", token)
	return mux.SetURLVars(req, map[string]string{"id": "1"})
}

func sessionRequest(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req, _ := http.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("X-Auth", token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// GHSA-6759-996p-gpj6: signup with createUserDir disabled and the factory
// default root scope must be refused, and must not create a root-scoped user.
func TestSignupRequiresDerivedScope(t *testing.T) {
	key := []byte("test-signing-key")
	st, server := newSessionStore(t, users.Permissions{Download: true}, key)

	if err := st.Settings.Save(&settings.Settings{
		Key:                   key,
		Signup:                true,
		CreateUserDir:         false,
		UserHomeBasePath:      "/users",
		MinimumPasswordLength: 1,
		Defaults: settings.UserDefaults{
			Scope: ".",
			Perm:  users.Permissions{Download: true},
		},
	}); err != nil {
		t.Fatal(err)
	}

	rec := sessionRequest(handle(signupHandler, "", st, server), http.MethodPost, "/signup", "",
		`{"username":"attacker","password":"AttackerPass123!"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("VULNERABLE: signup with an underivable scope returned %d, body=%q; want 403", rec.Code, rec.Body.String())
	}

	if _, err := st.Users.GetByScope("/"); !errors.Is(err, fberrors.ErrNotExist) {
		t.Fatalf("VULNERABLE: a root-scoped user was created: %v", err)
	}
	if _, err := st.Users.Get(server.Root, false, "attacker"); !errors.Is(err, fberrors.ErrNotExist) {
		t.Fatalf("VULNERABLE: signup persisted the user despite being refused: %v", err)
	}
}

// GHSA-6759-996p-gpj6: a self-registered user must not inherit the destructive
// default permissions. Download only is the safe default.
func TestSignupLeastPrivilege(t *testing.T) {
	key := []byte("test-signing-key")
	st, server := newSessionStore(t, users.Permissions{Download: true}, key)

	if err := st.Settings.Save(&settings.Settings{
		Key:                   key,
		Signup:                true,
		CreateUserDir:         true,
		UserHomeBasePath:      "/users",
		MinimumPasswordLength: 1,
		Defaults: settings.UserDefaults{
			Scope: ".",
			Perm: users.Permissions{
				Admin: true, Execute: true, Create: true, Rename: true,
				Modify: true, Delete: true, Share: true, Download: true,
			},
			Commands: []string{"ls", "id"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	rec := sessionRequest(handle(signupHandler, "", st, server), http.MethodPost, "/signup", "",
		`{"username":"newsignup","password":"SignupPass123!"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("signup returned %d, body=%q; want 200", rec.Code, rec.Body.String())
	}

	u, err := st.Users.Get(server.Root, false, "newsignup")
	if err != nil {
		t.Fatal(err)
	}
	if u.Perm.Admin || u.Perm.Execute || u.Perm.Create || u.Perm.Rename || u.Perm.Modify || u.Perm.Delete || u.Perm.Share {
		t.Errorf("VULNERABLE: signup inherited destructive defaults: %+v", u.Perm)
	}
	if !u.Perm.Download {
		t.Error("signup user should have Download permission")
	}
	if len(u.Commands) != 0 {
		t.Errorf("signup user should have no commands, got %v", u.Commands)
	}
	if u.Scope == "/" {
		t.Error("signup user must not share the server root")
	}
}

// An explicit signupPermissions block is the documented opt-in for extra
// permissions; Admin and Execute stay withheld even when requested.
func TestSignupPermissionsOptIn(t *testing.T) {
	key := []byte("test-signing-key")
	st, server := newSessionStore(t, users.Permissions{Download: true}, key)

	if err := st.Settings.Save(&settings.Settings{
		Key:                   key,
		Signup:                true,
		CreateUserDir:         true,
		UserHomeBasePath:      "/users",
		MinimumPasswordLength: 1,
		Defaults:              settings.UserDefaults{Scope: "."},
		SignupPermissions: &users.Permissions{
			Admin: true, Execute: true, Create: true, Download: true,
		},
	}); err != nil {
		t.Fatal(err)
	}

	rec := sessionRequest(handle(signupHandler, "", st, server), http.MethodPost, "/signup", "",
		`{"username":"optin","password":"SignupPass123!"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("signup returned %d, body=%q; want 200", rec.Code, rec.Body.String())
	}
	u, err := st.Users.Get(server.Root, false, "optin")
	if err != nil {
		t.Fatal(err)
	}
	if !u.Perm.Create || !u.Perm.Download {
		t.Errorf("opted-in permissions not applied: %+v", u.Perm)
	}
	if u.Perm.Admin || u.Perm.Execute {
		t.Errorf("Admin and Execute must never be granted via signup: %+v", u.Perm)
	}
}
