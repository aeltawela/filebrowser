package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/asdine/storm/v3"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/storage"
	"github.com/filebrowser/filebrowser/v2/storage/bolt"
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
