package auth

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"

	fberrors "github.com/filebrowser/filebrowser/v2/errors"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/users"
)

// MethodProxyAuth is used to identify no auth.
const MethodProxyAuth settings.AuthMethod = "proxy"

// ProxySecretHeader is the header a reverse proxy must set to the value of
// ProxyAuth.Secret when a shared secret is configured.
const ProxySecretHeader = "X-Proxy-Secret"

// ProxyAuth is a proxy implementation of an auther.
//
// A username in Header is an identity assertion made by the reverse proxy. It
// is only trusted when the request comes from a configured trusted CIDR or
// carries the shared secret. Provisioning an unknown username is opt-in and
// must still yield a per-user (or otherwise explicitly configured) scope.
type ProxyAuth struct {
	Header string `json:"header"`

	// TrustedProxies is a list of CIDRs whose source address may assert a
	// username. RemoteAddr is used deliberately: X-Forwarded-For and similar
	// headers are client-controlled and must not be treated as a trust anchor.
	TrustedProxies []string `json:"trustedProxies,omitempty"`

	// Secret, when set, must be echoed by the proxy in ProxySecretHeader.
	Secret string `json:"secret,omitempty"`

	// CreateUser allows a trusted proxy to provision previously unknown users.
	// It is off by default: an identity assertion must not be an account
	// creation primitive.
	CreateUser bool `json:"createUser,omitempty"`

	// AllowUntrusted is an explicit acknowledgement that the instance is only
	// reachable from trusted clients (e.g. bound to a loopback address). It is
	// checked at startup; at runtime a request still needs at least one trust
	// anchor unless this override was set.
	AllowUntrusted bool `json:"allowUntrusted,omitempty"`
}

// ValidateTrustConfig is called during startup so that proxy auth can never be
// enabled with an unverifiable identity source by accident.
func (a ProxyAuth) ValidateTrustConfig() error {
	if len(a.TrustedProxies) == 0 && a.Secret == "" {
		if !a.AllowUntrusted {
			return fmt.Errorf("auth.method=proxy requires auth.proxy.trustedProxies or auth.proxy.secret; set auth.proxy.allowUntrusted=true to accept an unauthenticated identity source explicitly")
		}
		return nil
	}

	for _, cidr := range a.TrustedProxies {
		if _, _, err := net.ParseCIDR(cidr); err != nil {
			return fmt.Errorf("invalid auth.proxy.trustedProxies entry %q: %w", cidr, err)
		}
	}
	return nil
}

// Trusted reports whether the request's identity assertion may be believed:
// the request must come from a configured trusted source, present the shared
// secret, or be explicitly allowed. It is the exported form of trusted for
// HTTP-layer checks (such as proxy session renewal) that must apply the same
// trust anchor as login.
func (a ProxyAuth) Trusted(r *http.Request) bool {
	return a.trusted(r)
}

// trusted reports whether the request's identity assertion may be believed.
func (a ProxyAuth) trusted(r *http.Request) bool {
	hasCIDRs := len(a.TrustedProxies) > 0
	hasSecret := a.Secret != ""

	if !hasCIDRs && !hasSecret {
		// Only the explicit unsafe override allows header auth with no anchor.
		return a.AllowUntrusted
	}

	if hasCIDRs {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = strings.Trim(r.RemoteAddr, "[]")
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return false
		}

		allowed := false
		for _, cidr := range a.TrustedProxies {
			_, network, err := net.ParseCIDR(cidr)
			if err != nil {
				continue // rejected at startup; never fail open here
			}
			if network.Contains(ip) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}

	if hasSecret && subtle.ConstantTimeCompare([]byte(r.Header.Get(ProxySecretHeader)), []byte(a.Secret)) != 1 {
		return false
	}

	return true
}

// Auth authenticates the user via an HTTP header asserted by a trusted proxy.
func (a ProxyAuth) Auth(r *http.Request, usr users.Store, setting *settings.Settings, srv *settings.Server) (*users.User, error) {
	if !a.trusted(r) {
		return nil, os.ErrPermission
	}

	username := strings.TrimSpace(r.Header.Get(a.Header))
	if username == "" {
		return nil, os.ErrPermission
	}

	user, err := usr.Get(srv.Root, srv.FollowExternalSymlinks, username)
	if errors.Is(err, fberrors.ErrNotExist) {
		if !a.CreateUser {
			return nil, os.ErrPermission
		}
		return a.createUser(usr, setting, srv, username)
	}
	return user, err
}

func (a ProxyAuth) createUser(usr users.Store, setting *settings.Settings, srv *settings.Server, username string) (*users.User, error) {
	const randomPasswordLength = settings.DefaultMinimumPasswordLength + 10
	pwd, err := users.RandomPwd(randomPasswordLength)
	if err != nil {
		return nil, err
	}

	var hashedRandomPassword string
	hashedRandomPassword, err = users.ValidateAndHashPwd(pwd, setting.MinimumPasswordLength)
	if err != nil {
		return nil, err
	}

	user := &users.User{
		Username:     username,
		Password:     hashedRandomPassword,
		LockPassword: true,
	}
	setting.Defaults.Apply(user)
	user.Perm.Admin = false
	user.Perm.Execute = false
	user.Commands = []string{}

	var derivedScope bool
	if derivedScope, err = setting.CreateUserHome(user, srv.Root, false); err != nil {
		return nil, err
	}

	if err = usr.SaveProvisioned(user, derivedScope); err != nil {
		return nil, err
	}

	return user, nil
}

// LoginPage tells that proxy auth doesn't require a login page.
func (a ProxyAuth) LoginPage() bool {
	return false
}
