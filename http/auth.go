package fbhttp

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/golang-jwt/jwt/v5/request"

	fbAuth "github.com/filebrowser/filebrowser/v2/auth"
	fberrors "github.com/filebrowser/filebrowser/v2/errors"
	"github.com/filebrowser/filebrowser/v2/files"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/users"
)

const (
	DefaultTokenExpirationTime = time.Hour * 2

	maxAuthBodySize = 1 << 20 // 1 MiB
)

type userInfo struct {
	ID                    uint              `json:"id"`
	Locale                string            `json:"locale"`
	ViewMode              users.ViewMode    `json:"viewMode"`
	SingleClick           bool              `json:"singleClick"`
	RedirectAfterCopyMove bool              `json:"redirectAfterCopyMove"`
	Perm                  users.Permissions `json:"perm"`
	Commands              []string          `json:"commands"`
	LockPassword          bool              `json:"lockPassword"`
	HideDotfiles          bool              `json:"hideDotfiles"`
	DateFormat            bool              `json:"dateFormat"`
	Username              string            `json:"username"`
	AceEditorTheme        string            `json:"aceEditorTheme"`
	Bookmarks             []users.Bookmark  `json:"bookmarks"`
}

type authToken struct {
	User userInfo `json:"user"`
	// Ver is the user's session epoch at the time the token was minted. A
	// missing claim decodes to zero, which is only valid while the stored
	// version is still zero (backwards compatibility for existing tokens).
	Ver uint64 `json:"ver,omitempty"`
	jwt.RegisteredClaims
}

type extractor []string

func (e extractor) ExtractToken(r *http.Request) (string, error) {
	token, _ := request.HeaderExtractor{"X-Auth"}.ExtractToken(r)

	// Checks if the token isn't empty and if it contains two dots.
	// The former prevents incompatibility with URLs that previously
	// used basic auth.
	if token != "" && strings.Count(token, ".") == 2 {
		return token, nil
	}

	if r.Method == http.MethodGet {
		cookie, _ := r.Cookie("auth")
		if cookie != nil && strings.Count(cookie.Value, ".") == 2 {
			return cookie.Value, nil
		}
	}

	return "", request.ErrNoTokenInRequest
}

func renewableErr(err error, r *http.Request, d *data, tk *authToken) bool {
	if d.settings.AuthMethod != fbAuth.MethodProxyAuth || err == nil {
		return false
	}

	if d.settings.LogoutPage == settings.DefaultLogoutPage {
		return false
	}

	if !errors.Is(err, jwt.ErrTokenExpired) {
		return false
	}

	// The expiration is only waived because the trusted proxy, not the token,
	// decides when the session ends. Require the proxy to still assert the same
	// identity on this request, otherwise a token that leaked before it expired
	// would authenticate on its own forever.
	return proxyAsserts(r, d, tk.User.ID)
}

// proxyAsserts reports whether the proxy-auth header on r identifies the user
// the token was issued for. The username is resolved through the user store, so
// that it is matched exactly as a regular proxy login would match it.
func proxyAsserts(r *http.Request, d *data, id uint) bool {
	auther, err := d.store.Auth.Get(fbAuth.MethodProxyAuth)
	if err != nil {
		return false
	}

	proxy, ok := auther.(*fbAuth.ProxyAuth)
	if !ok || proxy.Header == "" {
		return false
	}

	// The expiry waiver below trusts the proxy's identity assertion, so it
	// must apply the same trust anchor as login: a direct client spoofing the
	// header must not be able to keep an expired token alive.
	if !proxy.Trusted(r) {
		return false
	}

	username := r.Header.Get(proxy.Header)
	if username == "" {
		return false
	}

	user, err := d.store.Users.Get(d.server.Root, d.server.FollowExternalSymlinks, username)
	if err != nil {
		return false
	}

	return user.ID == id
}

func withUser(fn handleFunc) handleFunc {
	return func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
		keyFunc := func(_ *jwt.Token) (interface{}, error) {
			return d.settings.Key, nil
		}

		var tk authToken
		p := jwt.NewParser(jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
		token, err := request.ParseFromRequest(r, &extractor{}, keyFunc, request.WithClaims(&tk), request.WithParser(p))
		if (err != nil || !token.Valid) && !renewableErr(err, r, d, &tk) {
			return http.StatusUnauthorized, nil
		}

		expiresSoon := tk.ExpiresAt != nil && time.Until(tk.ExpiresAt.Time) < time.Hour
		updated := tk.IssuedAt != nil && tk.IssuedAt.Unix() < d.store.Users.LastUpdate(tk.User.ID)

		if expiresSoon || updated {
			w.Header().Add("X-Renew-Token", "true")
		}

		d.user, err = d.store.Users.Get(d.server.Root, d.server.FollowExternalSymlinks, tk.User.ID)
		if err != nil {
			// A token for a deleted user is an authentication failure, not a
			// server error: fail closed without tripping 5xx alerting or
			// leaking more than user (non-)existence already implies.
			if errors.Is(err, fberrors.ErrNotExist) {
				return http.StatusUnauthorized, nil
			}
			return http.StatusInternalServerError, err
		}
		files.SetRuleChecker(d.user.Fs, d)

		// Revocation check on every authenticated request: logout, password
		// changes, permission changes and account locking advance the stored
		// epoch, so any token minted before that point stops working at once.
		// This also runs for renewal and for the proxy-expiration exception.
		if d.user.TokenVersion != tk.Ver {
			return http.StatusUnauthorized, nil
		}

		canonicalizeRequestPath(r)
		return fn(w, r, d)
	}
}

func withAdmin(fn handleFunc) handleFunc {
	return withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
		if !d.user.Perm.Admin {
			return http.StatusForbidden, nil
		}

		return fn(w, r, d)
	})
}

func loginHandler(tokenExpireTime time.Duration) handleFunc {
	return func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodySize)
		}

		auther, err := d.store.Auth.Get(d.settings.AuthMethod)
		if err != nil {
			return http.StatusInternalServerError, err
		}

		user, err := auther.Auth(r, d.store.Users, d.settings, d.server)
		switch {
		case errors.Is(err, os.ErrPermission), errors.Is(err, settings.ErrRootScope):
			return http.StatusForbidden, nil
		case err != nil:
			return http.StatusInternalServerError, err
		}

		return printToken(w, r, d, user, tokenExpireTime)
	}
}

type signupBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

var signupHandler = func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	if !d.settings.Signup {
		return http.StatusMethodNotAllowed, nil
	}

	if r.Body == nil {
		return http.StatusBadRequest, nil
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodySize)

	info := &signupBody{}
	err := json.NewDecoder(r.Body).Decode(info)
	if err != nil {
		return http.StatusBadRequest, err
	}

	if info.Password == "" || info.Username == "" {
		return http.StatusBadRequest, nil
	}

	user := &users.User{
		Username: info.Username,
	}

	d.settings.Defaults.Apply(user)

	// Self-registration is least-privilege by default: download only. The
	// default permission set (which includes the destructive CRUD rights) is
	// never inherited. An administrator can opt in to additional rights with
	// the explicit SignupPermissions block, but Admin and Execute are always
	// withheld here and must be granted through a separate admin action.
	perms := users.Permissions{Download: true}
	if d.settings.SignupPermissions != nil {
		perms = *d.settings.SignupPermissions
		perms.Admin = false
		perms.Execute = false
	}
	user.Perm = perms
	user.Commands = []string{}

	pwd, err := users.ValidateAndHashPwd(info.Password, d.settings.MinimumPasswordLength)
	if err != nil {
		return http.StatusBadRequest, err
	}

	user.Password = pwd

	derivedScope, err := d.settings.CreateUserHome(user, d.server.Root, false)
	if errors.Is(err, settings.ErrRootScope) {
		log.Printf("configuration error: signup is enabled but no per-user scope can be derived; enable createUserDir or configure a non-root defaults.scope")
		return http.StatusForbidden, nil
	}
	if err != nil {
		return http.StatusInternalServerError, err
	}

	log.Printf("new user: %s, home dir: [%s].", user.Username, user.Scope)

	err = d.store.Users.SaveProvisioned(user, derivedScope)
	if errors.Is(err, fberrors.ErrExist) {
		return http.StatusConflict, err
	} else if err != nil {
		return http.StatusInternalServerError, err
	}

	return http.StatusOK, nil
}

// logoutHandler revokes every token previously issued to the current user by
// advancing the persisted session epoch. The client must still discard its
// local token; this handler makes replay of that token impossible.
var logoutHandler = withUser(func(w http.ResponseWriter, _ *http.Request, d *data) (int, error) {
	if err := d.store.Users.IncrementTokenVersion(d.user.ID); err != nil {
		return http.StatusInternalServerError, err
	}
	w.WriteHeader(http.StatusNoContent)
	return 0, nil
})

func renewHandler(tokenExpireTime time.Duration) handleFunc {
	return withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
		w.Header().Set("X-Renew-Token", "false")
		return printToken(w, r, d, d.user, tokenExpireTime)
	})
}

func printToken(w http.ResponseWriter, _ *http.Request, d *data, user *users.User, tokenExpirationTime time.Duration) (int, error) {
	// Every minted token carries a unique ID: without one, two logins in the
	// same second produce byte-identical tokens (same claims, second-precision
	// timestamps), and concurrent sessions would unknowingly share a single
	// token string.
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return http.StatusInternalServerError, err
	}
	claims := &authToken{
		User: userInfo{
			ID:                    user.ID,
			Locale:                user.Locale,
			ViewMode:              user.ViewMode,
			SingleClick:           user.SingleClick,
			RedirectAfterCopyMove: user.RedirectAfterCopyMove,
			Perm:                  user.Perm,
			LockPassword:          user.LockPassword,
			Commands:              user.Commands,
			HideDotfiles:          user.HideDotfiles,
			DateFormat:            user.DateFormat,
			Username:              user.Username,
			AceEditorTheme:        user.AceEditorTheme,
			Bookmarks:             user.Bookmarks,
		},
		Ver: user.TokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        base64.RawURLEncoding.EncodeToString(idBytes[:]),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(tokenExpirationTime)),
			Issuer:    "File Browser",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(d.settings.Key)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	w.Header().Set("Content-Type", "text/plain")
	if _, err := w.Write([]byte(signed)); err != nil {
		return http.StatusInternalServerError, err
	}
	return 0, nil
}
