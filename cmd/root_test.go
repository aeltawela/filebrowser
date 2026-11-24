package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/asdine/storm/v3"
	"github.com/filebrowser/filebrowser/v2/auth"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/storage"
	"github.com/filebrowser/filebrowser/v2/storage/bolt"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// newSignupStartupStore builds a database with the given global settings,
// mirroring the startup path in getServerSettings.
func newSignupStartupStore(t *testing.T, set *settings.Settings) *storage.Storage {
	t.Helper()
	db, err := storm.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	st, err := bolt.NewStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	set.Key = []byte("test-signing-key")
	if err := st.Settings.Save(set); err != nil {
		t.Fatal(err)
	}
	if err := st.Settings.SaveServer(&settings.Server{}); err != nil {
		t.Fatal(err)
	}
	return st
}

// GHSA-6759-996p-gpj6: an instance with signup enabled but no derivable
// per-user scope must refuse to start, not just warn. The signup handler
// already refuses such registrations; the startup gate surfaces the
// misconfiguration at deploy time.
func TestSignupStartupRefusesRootScope(t *testing.T) {
	t.Run("root default scope is refused", func(t *testing.T) {
		st := newSignupStartupStore(t, &settings.Settings{Signup: true})
		_, err := getServerSettings(viper.New(), st)
		if err == nil {
			t.Fatal("VULNERABLE: startup accepted signup with a root default scope")
		}
		if !strings.Contains(err.Error(), "createUserDir") {
			t.Errorf("error should explain the configuration fix, got %q", err)
		}
	})

	t.Run("createUserDir is accepted", func(t *testing.T) {
		st := newSignupStartupStore(t, &settings.Settings{Signup: true, CreateUserDir: true})
		if _, err := getServerSettings(viper.New(), st); err != nil {
			t.Fatalf("derived per-user homes must be accepted, got %v", err)
		}
	})

	t.Run("explicit non-root scope is accepted", func(t *testing.T) {
		st := newSignupStartupStore(t, &settings.Settings{
			Signup:   true,
			Defaults: settings.UserDefaults{Scope: "/shared"},
		})
		if _, err := getServerSettings(viper.New(), st); err != nil {
			t.Fatalf("explicit non-root scope must be accepted, got %v", err)
		}
	})

	t.Run("disabled signup is unaffected", func(t *testing.T) {
		st := newSignupStartupStore(t, &settings.Settings{Signup: false})
		if _, err := getServerSettings(viper.New(), st); err != nil {
			t.Fatalf("disabled signup must not be gated, got %v", err)
		}
	})
}

// newProxyStartupStore builds a database configured for proxy authentication
// with the given auther, mirroring the startup path in rootCmd.RunE.
func newProxyStartupStore(t *testing.T, auther *auth.ProxyAuth) *storage.Storage {
	t.Helper()
	db, err := storm.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	st, err := bolt.NewStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Settings.Save(&settings.Settings{
		Key: []byte("test-signing-key"), AuthMethod: auth.MethodProxyAuth,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Auth.Save(auther); err != nil {
		t.Fatal(err)
	}
	return st
}

// GHSA-xqp3-jq6g-x3qm: the startup check must refuse to run proxy auth with no
// trust anchor. A warning in the log is not enough.
func TestProxyAuthRequiresTrustConfig(t *testing.T) {
	t.Run("no trust anchor is refused", func(t *testing.T) {
		st := newProxyStartupStore(t, &auth.ProxyAuth{Header: "X-Remote-User"})
		err := validateProxyAuthConfig(st)
		if err == nil {
			t.Fatal("VULNERABLE: proxy auth started with no trusted proxy or secret")
		}
		if !strings.Contains(err.Error(), "trustedProxies") {
			t.Errorf("error should explain the configuration fix, got %q", err)
		}
	})

	t.Run("trusted CIDR is accepted", func(t *testing.T) {
		st := newProxyStartupStore(t, &auth.ProxyAuth{
			Header: "X-Remote-User", TrustedProxies: []string{"127.0.0.1/32"},
		})
		if err := validateProxyAuthConfig(st); err != nil {
			t.Fatalf("trusted proxy config rejected: %v", err)
		}
	})

	t.Run("shared secret is accepted", func(t *testing.T) {
		st := newProxyStartupStore(t, &auth.ProxyAuth{
			Header: "X-Remote-User", Secret: "shared-secret",
		})
		if err := validateProxyAuthConfig(st); err != nil {
			t.Fatalf("secret config rejected: %v", err)
		}
	})

	t.Run("explicit unsafe override is accepted", func(t *testing.T) {
		st := newProxyStartupStore(t, &auth.ProxyAuth{
			Header: "X-Remote-User", AllowUntrusted: true,
		})
		if err := validateProxyAuthConfig(st); err != nil {
			t.Fatalf("explicit override rejected: %v", err)
		}
	})

	t.Run("invalid CIDR is refused", func(t *testing.T) {
		st := newProxyStartupStore(t, &auth.ProxyAuth{
			Header: "X-Remote-User", TrustedProxies: []string{"not-a-cidr"},
		})
		if err := validateProxyAuthConfig(st); err == nil {
			t.Fatal("invalid trusted proxy CIDR was accepted")
		}
	})
}

// The trust controls must be configurable through `config set`, not only by
// editing the database directly.
func TestProxyAuthTrustConfigFlags(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	addConfigFlags(flags)
	if err := flags.Parse([]string{
		"--auth.method=proxy",
		"--auth.header=X-Remote-User",
		"--auth.proxy.trustedProxies=10.0.0.0/8,192.168.0.0/16",
		"--auth.proxy.secret=s3cret",
		"--auth.proxy.createUser",
	}); err != nil {
		t.Fatal(err)
	}

	set := &settings.Settings{}
	auther, err := getSettings(flags, set, &settings.Server{}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if set.AuthMethod != auth.MethodProxyAuth {
		t.Fatalf("auth method = %q, want proxy", set.AuthMethod)
	}
	proxy, ok := auther.(*auth.ProxyAuth)
	if !ok {
		t.Fatalf("auther has type %T, want *auth.ProxyAuth", auther)
	}
	if got := strings.Join(proxy.TrustedProxies, ","); got != "10.0.0.0/8,192.168.0.0/16" {
		t.Errorf("trusted proxies = %q", got)
	}
	if proxy.Secret != "s3cret" || !proxy.CreateUser {
		t.Errorf("secret=%q createUser=%t", proxy.Secret, proxy.CreateUser)
	}
}

// An explicit false/empty proxy trust setting must actually clear the stored
// value instead of being mistaken for "flag not passed" (which would silently
// keep the more permissive stored configuration live).
func TestProxyTrustSettingsCanBeCleared(t *testing.T) {
	stored := map[string]interface{}{
		"header":         "X-Remote-User",
		"trustedProxies": []interface{}{"10.0.0.0/8"},
		"secret":         "old-secret",
		"createUser":     true,
		"allowUntrusted": true,
	}

	t.Run("unset flags preserve stored values", func(t *testing.T) {
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		addConfigFlags(flags)
		if err := flags.Parse([]string{"--auth.header=X-Remote-User"}); err != nil {
			t.Fatal(err)
		}
		auther, err := getProxyAuth(flags, stored)
		if err != nil {
			t.Fatal(err)
		}
		proxy := auther.(*auth.ProxyAuth)
		if len(proxy.TrustedProxies) != 1 || proxy.Secret != "old-secret" || !proxy.CreateUser || !proxy.AllowUntrusted {
			t.Fatalf("unset flags must preserve stored values, got %+v", proxy)
		}
	})

	t.Run("explicit false and empty clear stored values", func(t *testing.T) {
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		addConfigFlags(flags)
		if err := flags.Parse([]string{
			"--auth.header=X-Remote-User",
			"--auth.proxy.trustedProxies=",
			"--auth.proxy.secret=",
			"--auth.proxy.createUser=false",
			"--auth.proxy.allowUntrusted=false",
		}); err != nil {
			t.Fatal(err)
		}
		auther, err := getProxyAuth(flags, stored)
		if err != nil {
			t.Fatal(err)
		}
		proxy := auther.(*auth.ProxyAuth)
		if len(proxy.TrustedProxies) != 0 {
			t.Errorf("VULNERABLE: trusted proxies not cleared: %q", proxy.TrustedProxies)
		}
		if proxy.Secret != "" {
			t.Error("VULNERABLE: leaked secret not cleared")
		}
		if proxy.CreateUser || proxy.AllowUntrusted {
			t.Errorf("VULNERABLE: createUser=%t allowUntrusted=%t not cleared", proxy.CreateUser, proxy.AllowUntrusted)
		}
	})
}

// `config cat` output ends up in docs and tickets: shared secrets must be
// redacted, including nested ones such as the reCAPTCHA secret.
func TestRedactSecrets(t *testing.T) {
	shown := map[string]interface{}{
		"header": "X-Remote-User",
		"secret": "s3cret",
		"recaptcha": map[string]interface{}{
			"key":    "site-key",
			"secret": "private-key",
		},
		"empty": map[string]interface{}{"secret": ""},
	}
	redactSecrets(shown)
	if shown["secret"] != "***REDACTED***" {
		t.Errorf("proxy secret not redacted: %v", shown["secret"])
	}
	nested := shown["recaptcha"].(map[string]interface{})
	if nested["secret"] != "***REDACTED***" || nested["key"] != "site-key" {
		t.Errorf("nested redaction wrong: %v", nested)
	}
	if shown["empty"].(map[string]interface{})["secret"] != "" {
		t.Errorf("empty secret should stay empty: %v", shown["empty"])
	}
}

// A stored reCAPTCHA secret must be revocable via `config set`: an explicitly
// emptied value clears it instead of silently keeping the stored one.
func TestRecaptchaSecretCanBeCleared(t *testing.T) {
	stored := map[string]interface{}{
		"recaptcha": map[string]interface{}{"key": "site", "secret": "old-secret"},
	}

	t.Run("unset flags preserve stored values", func(t *testing.T) {
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		addConfigFlags(flags)
		if err := flags.Parse([]string{}); err != nil {
			t.Fatal(err)
		}
		auther, err := getJSONAuth(flags, stored)
		if err != nil {
			t.Fatal(err)
		}
		jsonAuth := auther.(*auth.JSONAuth)
		if jsonAuth.ReCaptcha == nil || jsonAuth.ReCaptcha.Secret != "old-secret" {
			t.Fatalf("unset flags must preserve the stored secret, got %+v", jsonAuth.ReCaptcha)
		}
	})

	t.Run("explicit empty clears the stored secret", func(t *testing.T) {
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		addConfigFlags(flags)
		if err := flags.Parse([]string{"--recaptcha.secret="}); err != nil {
			t.Fatal(err)
		}
		auther, err := getJSONAuth(flags, stored)
		if err != nil {
			t.Fatal(err)
		}
		jsonAuth := auther.(*auth.JSONAuth)
		if jsonAuth.ReCaptcha != nil {
			t.Errorf("explicit empty secret must clear reCAPTCHA config, got %+v", jsonAuth.ReCaptcha)
		}
	})
}

// A malformed stored reCAPTCHA block must not panic the config path either.
func TestRecaptchaMalformedBlockReturnsEmpty(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	addConfigFlags(flags)
	if err := flags.Parse([]string{}); err != nil {
		t.Fatal(err)
	}
	stored := map[string]interface{}{"recaptcha": map[string]interface{}{"key": 123, "secret": 456}}
	auther, err := getJSONAuth(flags, stored)
	if err != nil {
		t.Fatal(err)
	}
	if auther.(*auth.JSONAuth).ReCaptcha != nil {
		t.Error("malformed reCAPTCHA block should yield no reCAPTCHA config, not a panic")
	}
}
