package fbhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	fbAuth "github.com/filebrowser/filebrowser/v2/auth"
	fberrors "github.com/filebrowser/filebrowser/v2/errors"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/storage"
	"github.com/filebrowser/filebrowser/v2/users"
	"github.com/golang-jwt/jwt/v5"
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

// GHSA-xqp3-jq6g-x3qm: a proxy header from an address outside the configured
// trusted CIDRs must never authenticate (or mint a JWT for) any user.
func TestProxyAuthRejectsUntrustedPeer(t *testing.T) {
	key := []byte("test-signing-key")
	perm := users.Permissions{Download: true}
	st, server := newSessionStore(t, perm, key)

	if err := st.Settings.Save(&settings.Settings{
		Key: key, AuthMethod: fbAuth.MethodProxyAuth, LogoutPage: settings.DefaultLogoutPage,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Auth.Save(&fbAuth.ProxyAuth{
		Header:         "X-Remote-User",
		TrustedProxies: []string{"10.0.0.0/8"},
	}); err != nil {
		t.Fatal(err)
	}

	login := handle(loginHandler(time.Hour), "", st, server)
	attempt := func(remoteAddr string) *httptest.ResponseRecorder {
		req, _ := http.NewRequest(http.MethodPost, "/login", strings.NewReader(`{}`))
		req.Header.Set("X-Remote-User", "u")
		req.RemoteAddr = remoteAddr
		rec := httptest.NewRecorder()
		login.ServeHTTP(rec, req)
		return rec
	}

	if rec := attempt("203.0.113.10:1234"); rec.Code != http.StatusForbidden {
		t.Errorf("VULNERABLE: header from an untrusted peer returned %d; want 403", rec.Code)
	}
	if rec := attempt("10.1.2.3:1234"); rec.Code != http.StatusOK {
		t.Errorf("trusted proxy header returned %d, body=%q; want 200", rec.Code, rec.Body.String())
	}
}

// A configured shared secret is required on every request: a wrong or missing
// secret must reject the header even from a trusted address.
func TestProxyAuthRequiresSecret(t *testing.T) {
	key := []byte("test-signing-key")
	st, server := newSessionStore(t, users.Permissions{Download: true}, key)

	if err := st.Settings.Save(&settings.Settings{
		Key: key, AuthMethod: fbAuth.MethodProxyAuth, LogoutPage: settings.DefaultLogoutPage,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Auth.Save(&fbAuth.ProxyAuth{
		Header:         "X-Remote-User",
		TrustedProxies: []string{"10.0.0.0/8"},
		Secret:         "shared-secret",
	}); err != nil {
		t.Fatal(err)
	}

	login := handle(loginHandler(time.Hour), "", st, server)
	attempt := func(secret string) *httptest.ResponseRecorder {
		req, _ := http.NewRequest(http.MethodPost, "/login", strings.NewReader(`{}`))
		req.Header.Set("X-Remote-User", "u")
		req.Header.Set(fbAuth.ProxySecretHeader, secret)
		req.RemoteAddr = "10.1.2.3:1234"
		rec := httptest.NewRecorder()
		login.ServeHTTP(rec, req)
		return rec
	}

	if rec := attempt(""); rec.Code != http.StatusForbidden {
		t.Errorf("missing proxy secret returned %d; want 403", rec.Code)
	}
	if rec := attempt("wrong"); rec.Code != http.StatusForbidden {
		t.Errorf("wrong proxy secret returned %d; want 403", rec.Code)
	}
	if rec := attempt("shared-secret"); rec.Code != http.StatusOK {
		t.Errorf("correct proxy secret returned %d, body=%q; want 200", rec.Code, rec.Body.String())
	}
}

// GHSA-xqp3-jq6g-x3qm: an unknown username must not create an account unless
// provisioning was explicitly enabled.
func TestProxyAuthNoImplicitProvisioning(t *testing.T) {
	key := []byte("test-signing-key")
	st, server := newSessionStore(t, users.Permissions{Download: true}, key)

	if err := st.Settings.Save(&settings.Settings{
		Key: key, AuthMethod: fbAuth.MethodProxyAuth, LogoutPage: settings.DefaultLogoutPage,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Auth.Save(&fbAuth.ProxyAuth{
		Header:         "X-Remote-User",
		TrustedProxies: []string{"10.0.0.0/8"},
	}); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodPost, "/login", strings.NewReader(`{}`))
	req.Header.Set("X-Remote-User", "backdoor")
	req.RemoteAddr = "10.1.2.3:1234"
	rec := httptest.NewRecorder()
	handle(loginHandler(time.Hour), "", st, server).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("VULNERABLE: unknown proxy user returned %d; want 403", rec.Code)
	}
	if _, err := st.Users.Get(server.Root, false, "backdoor"); !errors.Is(err, fberrors.ErrNotExist) {
		t.Fatalf("VULNERABLE: proxy auth created an account: %v", err)
	}
}

// With explicit provisioning, a per-user scope must still be derived; a
// root-scoped default with createUserDir disabled must be refused.
func TestProxyProvisioningRefusesRootScope(t *testing.T) {
	key := []byte("test-signing-key")
	st, server := newSessionStore(t, users.Permissions{Download: true}, key)

	if err := st.Settings.Save(&settings.Settings{
		Key: key, AuthMethod: fbAuth.MethodProxyAuth, LogoutPage: settings.DefaultLogoutPage,
		CreateUserDir: false, UserHomeBasePath: "/users",
		Defaults: settings.UserDefaults{Scope: "."},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Auth.Save(&fbAuth.ProxyAuth{
		Header: "X-Remote-User", TrustedProxies: []string{"10.0.0.0/8"}, CreateUser: true,
	}); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodPost, "/login", strings.NewReader(`{}`))
	req.Header.Set("X-Remote-User", "provisioned")
	req.RemoteAddr = "10.1.2.3:1234"
	rec := httptest.NewRecorder()
	handle(loginHandler(time.Hour), "", st, server).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("VULNERABLE: provisioning with a root scope returned %d; want 403", rec.Code)
	}
	if _, err := st.Users.Get(server.Root, false, "provisioned"); !errors.Is(err, fberrors.ErrNotExist) {
		t.Fatalf("VULNERABLE: a root-scoped user was provisioned: %v", err)
	}
}

// The proxy renewal waiver trusts the identity header, so it must apply the
// same trust anchor as login: a direct client spoofing the header from an
// untrusted address must not be able to assert another user's identity, or a
// leaked expired token could be kept alive past the proxy.
func TestProxyAssertsRequiresTrustAnchor(t *testing.T) {
	key := []byte("test-signing-key")
	st, server := newSessionStore(t, users.Permissions{Download: true}, key)

	set := &settings.Settings{Key: key, AuthMethod: fbAuth.MethodProxyAuth}
	if err := st.Auth.Save(&fbAuth.ProxyAuth{
		Header:         "X-Remote-User",
		TrustedProxies: []string{"10.0.0.0/8"},
	}); err != nil {
		t.Fatal(err)
	}
	d := &data{settings: set, server: server, store: st}

	attempt := func(remoteAddr string) bool {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("X-Remote-User", "u")
		req.RemoteAddr = remoteAddr
		return proxyAsserts(req, d, 1)
	}

	if attempt("203.0.113.10:1234") {
		t.Error("VULNERABLE: identity header from an untrusted peer is accepted")
	}
	if !attempt("10.1.2.3:1234") {
		t.Error("identity header from a trusted proxy is rejected")
	}
}

func signTokenWithVersion(t *testing.T, key []byte, id uint, ver uint64) string {
	t.Helper()
	claims := &authToken{
		User: userInfo{ID: id, Username: "u", Perm: users.Permissions{Download: true}},
		Ver:  ver,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(key)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return signed
}

// GHSA-7xwp-2cpp-p8r7: logout must revoke the token server-side.
func TestLogoutRevokesToken(t *testing.T) {
	key := []byte("test-signing-key")
	perm := users.Permissions{Download: true}
	st, server := newSessionStore(t, perm, key)
	protected := protectedSessionHandler(st, server)
	logout := handle(logoutHandler, "", st, server)
	token := signToken(t, perm, key)

	if rec := sessionRequest(protected, http.MethodGet, "/", token, ""); rec.Code != http.StatusOK {
		t.Fatalf("token should be valid before logout, got %d", rec.Code)
	}

	rec := sessionRequest(logout, http.MethodPost, "/logout", token, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout returned %d, body=%q; want 204", rec.Code, rec.Body.String())
	}
	if rec := sessionRequest(protected, http.MethodGet, "/", token, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("VULNERABLE: revoked token still authenticates after logout: %d", rec.Code)
	}
}

// A forged or stale ver claim must be rejected; a token with no claim is only
// valid while the stored version is still zero.
func TestTokenVersionMismatchRejected(t *testing.T) {
	key := []byte("test-signing-key")
	perm := users.Permissions{Download: true}
	st, server := newSessionStore(t, perm, key)
	protected := protectedSessionHandler(st, server)

	if rec := sessionRequest(protected, http.MethodGet, "/", signTokenWithVersion(t, key, 1, 99), ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("VULNERABLE: a token with a mismatched ver was accepted: %d", rec.Code)
	}
	if rec := sessionRequest(protected, http.MethodGet, "/", signToken(t, perm, key), ""); rec.Code != http.StatusOK {
		t.Errorf("a claim-less token should be valid at version 0, got %d", rec.Code)
	}
}

// Renewal must re-read the stored version and must not extend a revoked token.
func TestRenewDoesNotResurrectRevokedToken(t *testing.T) {
	key := []byte("test-signing-key")
	perm := users.Permissions{Download: true}
	st, server := newSessionStore(t, perm, key)
	logout := handle(logoutHandler, "", st, server)
	token := signToken(t, perm, key)

	if rec := sessionRequest(logout, http.MethodPost, "/logout", token, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("logout returned %d", rec.Code)
	}
	renew := sessionRequest(handle(renewHandler(time.Hour), "", st, server), http.MethodPost, "/renew", token, "")
	if renew.Code != http.StatusUnauthorized {
		t.Errorf("VULNERABLE: revoked token was renewed: %d", renew.Code)
	}
}

// Hook auth rebuilds the user record from scratch on every login and then
// full-saves it. That save must preserve the live session epoch: otherwise a
// benign re-login resets the version to zero and undoes logout and
// password-reset revocation. And when the login does revoke (a sensitive field
// changed), the freshly minted token must carry the new epoch or login itself
// breaks.
func TestHookAuthLoginPreservesAndAdvancesEpoch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell hook")
	}

	key := []byte("test-signing-key")
	st, server := newSessionStore(t, users.Permissions{Download: true}, key)

	if err := st.Settings.Save(&settings.Settings{
		Key: key, AuthMethod: fbAuth.MethodHookAuth, LogoutPage: settings.DefaultLogoutPage,
		CreateUserDir: true, UserHomeBasePath: "/users",
	}); err != nil {
		t.Fatal(err)
	}
	writeScript := func(body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "hook.sh")
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// An innocuous extra field forces the update branch on every login without
	// changing anything security-sensitive.
	if err := st.Auth.Save(&fbAuth.HookAuth{Command: writeScript("echo hook.action=auth\necho user.locale=en\n")}); err != nil {
		t.Fatal(err)
	}

	login := handle(loginHandler(time.Hour), "", st, server)
	protected := protectedSessionHandler(st, server)
	logout := handle(logoutHandler, "", st, server)
	creds := `{"username":"hookuser","password":"HookPass123!"}`

	loginAs := func() string {
		t.Helper()
		rec := sessionRequest(login, http.MethodPost, "/login", "", creds)
		if rec.Code != http.StatusOK {
			t.Fatalf("hook login returned %d, body=%q; want 200", rec.Code, rec.Body.String())
		}
		return strings.TrimSpace(rec.Body.String())
	}
	mustAuthenticate := func(token string) {
		t.Helper()
		if rec := sessionRequest(protected, http.MethodGet, "/", token, ""); rec.Code != http.StatusOK {
			t.Fatalf("freshly minted token rejected with %d; want 200", rec.Code)
		}
	}
	mustReject := func(token, why string) {
		t.Helper()
		if rec := sessionRequest(protected, http.MethodGet, "/", token, ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("VULNERABLE: %s: token authenticates with %d; want 401", why, rec.Code)
		}
	}

	token1 := loginAs()
	mustAuthenticate(token1)

	if rec := sessionRequest(logout, http.MethodPost, "/logout", token1, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("logout returned %d, body=%q; want 204", rec.Code, rec.Body.String())
	}
	mustReject(token1, "token issued before logout")

	// Benign re-login: the same hook output changes nothing sensitive, so the
	// epoch stays at 1, the new token works, and the logged-out token stays
	// dead. Before the fix the full save reset the epoch to 0 and token1 came
	// back to life.
	token2 := loginAs()
	mustAuthenticate(token2)
	mustReject(token1, "revocation undone by benign hook re-login")

	// Sensitive change: the hook now returns a different scope, which revokes.
	// The new token must carry the bumped epoch (before the fix it carried a
	// stale zero and every request after login returned 401).
	if err := st.Auth.Save(&fbAuth.HookAuth{Command: writeScript("echo hook.action=auth\necho user.scope=/other\n")}); err != nil {
		t.Fatal(err)
	}
	token3 := loginAs()
	mustAuthenticate(token3)
	mustReject(token2, "token issued before the scope change")
}

// A token naming a user that no longer exists is an authentication failure,
// not a server error: it must fail closed with 401 without tripping 5xx
// alerting.
func TestTokenForDeletedUserIsUnauthorized(t *testing.T) {
	key := []byte("test-signing-key")
	st, server := newSessionStore(t, users.Permissions{Download: true}, key)
	protected := protectedSessionHandler(st, server)

	ghost := signTokenWithVersion(t, key, 999, 0)
	if rec := sessionRequest(protected, http.MethodGet, "/", ghost, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("token for a deleted user returned %d; want 401", rec.Code)
	}
}

// Changing a user's access rules revokes outstanding tokens, like any other
// permission change: the stored rules are part of what the token authorizes.
func TestRuleChangeRevokesToken(t *testing.T) {
	key := []byte("test-signing-key")
	perm := users.Permissions{Download: true, Admin: true}
	st, server := newSessionStore(t, perm, key)
	protected := protectedSessionHandler(st, server)
	token := signToken(t, perm, key)

	put := handle(userPutHandler, "", st, server)
	body := `{"what":"user","which":["rules"],"data":{"id":1,"rules":[{"path":"/secret","allow":false}]}}`
	rec := httptest.NewRecorder()
	put.ServeHTTP(rec, putUserRequest(token, body))
	if rec.Code != http.StatusOK {
		t.Fatalf("rules update returned %d, body=%q; want 200", rec.Code, rec.Body.String())
	}
	if rec := sessionRequest(protected, http.MethodGet, "/", token, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("VULNERABLE: token survives an access-rule change: %d", rec.Code)
	}
}

// GHSA-v7vv-5wj2-gfcj: security-sensitive user updates must revoke all tokens
// previously issued to that user, including admin-initiated password resets.
func TestPasswordResetRevokesToken(t *testing.T) {
	key := []byte("test-signing-key")
	perm := users.Permissions{Download: true}
	st, server := newSessionStore(t, perm, key)
	protected := protectedSessionHandler(st, server)
	token := signToken(t, perm, key)

	if rec := sessionRequest(protected, http.MethodGet, "/", token, ""); rec.Code != http.StatusOK {
		t.Fatalf("token should be valid before reset, got %d", rec.Code)
	}

	put := handle(userPutHandler, "", st, server)
	reset := `{"what":"user","which":["password"],"data":{"id":1,"password":"NewStrongPass123!"}}`
	rec := httptest.NewRecorder()
	put.ServeHTTP(rec, putUserRequest(token, reset))
	if rec.Code != http.StatusOK {
		t.Fatalf("password reset returned %d, body=%q; want 200", rec.Code, rec.Body.String())
	}

	if rec := sessionRequest(protected, http.MethodGet, "/", token, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("VULNERABLE: token issued before password reset still authenticates: %d", rec.Code)
	}

	// Permission, lock, and scope changes must revoke as well.
	for name, field := range map[string]string{"perm": "perm", "lock": "lockPassword", "scope": "scope"} {
		t.Run(name, func(t *testing.T) {
			perm := users.Permissions{Admin: true, Download: true}
			st, server := newSessionStore(t, perm, key)
			protected := protectedSessionHandler(st, server)
			tok := signToken(t, perm, key)
			body := `{"what":"user","which":["` + field + `"],"data":{"id":1,"lockPassword":true,"perm":{"download":true},"scope":"/"}}`
			rec := httptest.NewRecorder()
			handle(userPutHandler, "", st, server).ServeHTTP(rec, putUserRequest(tok, body))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s update returned %d, body=%q", name, rec.Code, rec.Body.String())
			}
			if rec := sessionRequest(protected, http.MethodGet, "/", tok, ""); rec.Code != http.StatusUnauthorized {
				t.Errorf("VULNERABLE: token survives %s change: %d", name, rec.Code)
			}
		})
	}
}

// Brute-force protection: repeated failed logins from one peer are refused
// with 429 instead of letting credential stuffing run unbounded.
func TestLoginRateLimitBlocksAfterRepeatedFailures(t *testing.T) {
	loginAttempts.reset()
	t.Cleanup(loginAttempts.reset)

	key := []byte("test-signing-key")
	st, server := newSessionStore(t, users.Permissions{Download: true}, key)
	if err := st.Settings.Save(&settings.Settings{Key: key, AuthMethod: fbAuth.MethodJSONAuth}); err != nil {
		t.Fatal(err)
	}
	if err := st.Auth.Save(&fbAuth.JSONAuth{}); err != nil {
		t.Fatal(err)
	}
	login := handle(loginHandler(time.Hour), "", st, server)

	badCreds := `{"username":"u","password":"wrong"}`
	var last int
	for i := 0; i < 11; i++ {
		req, _ := http.NewRequest(http.MethodPost, "/login", strings.NewReader(badCreds))
		req.RemoteAddr = "198.51.100.7:1234"
		rec := httptest.NewRecorder()
		login.ServeHTTP(rec, req)
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("11th consecutive failure returned %d; want 429", last)
	}

	// A different peer is unaffected.
	req, _ := http.NewRequest(http.MethodPost, "/login", strings.NewReader(badCreds))
	req.RemoteAddr = "198.51.100.8:1234"
	rec := httptest.NewRecorder()
	login.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("other peer returned %d; want 403", rec.Code)
	}

	// And the standard security headers ride along on every handled response.
	for h, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
	} {
		if got := rec.Header().Get(h); got != want {
			t.Errorf("header %s = %q, want %q", h, got, want)
		}
	}
}

// Share-password guessing is bounded per share per peer: repeated wrong
// passwords yield 429 instead of an unbounded bcrypt oracle.
func TestSharePasswordRateLimitBlocksGuessing(t *testing.T) {
	sharePasswordAttempts.reset()
	t.Cleanup(sharePasswordAttempts.reset)

	root := t.TempDir()
	userScope := filepath.Join(root, "user")
	if err := os.MkdirAll(userScope, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userScope, "file.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Share: true, Download: true}
	st := scopedUserStorage(t, userScope, perm, key)
	server := &settings.Server{Root: root}

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

	var last int
	for i := 0; i < 11; i++ {
		r, _ := http.NewRequest(http.MethodGet, "/api/public/dl/"+created.Hash+"/file.txt", http.NoBody)
		r.Header.Set("X-SHARE-PASSWORD", url.QueryEscape("wrong-password"))
		r.RemoteAddr = "203.0.113.9:1234"
		w := httptest.NewRecorder()
		handle(publicDlHandler, "/api/public/dl/", st, server).ServeHTTP(w, r)
		last = w.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("11th wrong password returned %d; want 429", last)
	}

	// The correct password still works from another peer.
	r, _ := http.NewRequest(http.MethodGet, "/api/public/dl/"+created.Hash+"/file.txt", http.NoBody)
	r.Header.Set("X-SHARE-PASSWORD", url.QueryEscape("ShareSecret123!"))
	r.RemoteAddr = "203.0.113.10:1234"
	w := httptest.NewRecorder()
	handle(publicDlHandler, "/api/public/dl/", st, server).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("correct password from another peer returned %d; want 200", w.Code)
	}
}
