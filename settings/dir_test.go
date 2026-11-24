package settings

import (
	"errors"
	"testing"

	"github.com/filebrowser/filebrowser/v2/users"
)

// A user provisioned with CreateUserDir must receive a per-user home directory
// derived from its username, not the default scope which normalizes to the
// server root.
func TestCreateUserHomeDerivesPerUserScope(t *testing.T) {
	s := &Settings{CreateUserDir: true, UserHomeBasePath: "/users"}

	user := &users.User{Username: "alice", Scope: "."}
	derived, err := s.CreateUserHome(user, t.TempDir(), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !derived {
		t.Error("expected the scope to be reported as derived")
	}
	if user.Scope != "/users/alice" {
		t.Errorf("expected derived scope /users/alice, got %q", user.Scope)
	}
}

// GHSA-6759-996p-gpj6: provisioning must never hand out the server root just
// because createUserDir is disabled and the default scope normalizes to "/".
func TestCreateUserHomeRefusesRootScope(t *testing.T) {
	s := &Settings{CreateUserDir: false, UserHomeBasePath: "/users"}

	for _, scope := range []string{"", ".", "/"} {
		user := &users.User{Username: "alice", Scope: scope}
		derived, err := s.CreateUserHome(user, t.TempDir(), false)
		if !errors.Is(err, ErrRootScope) {
			t.Errorf("scope %q: expected ErrRootScope, got %v", scope, err)
		}
		if derived {
			t.Errorf("scope %q: must not report a derived scope", scope)
		}
	}
}

// A scope explicitly supplied by the caller (e.g. returned by an auth hook) must
// be preserved instead of being replaced by a derived home directory, and must
// not be reported as derived: it is legitimate for several users to share it.
func TestCreateUserHomePreservesExplicitScope(t *testing.T) {
	s := &Settings{CreateUserDir: true, UserHomeBasePath: "/users"}

	user := &users.User{Username: "alice", Scope: "/custom"}
	derived, err := s.CreateUserHome(user, t.TempDir(), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if derived {
		t.Error("an explicit scope must not be reported as derived")
	}
	if user.Scope != "/custom" {
		t.Errorf("explicit scope should be preserved, got %q", user.Scope)
	}
}

// An explicitly provided root scope is admin intent (only hook auth passes
// explicit scopes, from the administrator's own script) and must not be
// refused: administrators can already grant a root scope through the UI or
// CLI. The ErrRootScope refusal applies to implicit provisioning only.
func TestCreateUserHomeAllowsExplicitRootScope(t *testing.T) {
	s := &Settings{CreateUserDir: false, UserHomeBasePath: "/users"}

	user := &users.User{Username: "alice", Scope: "/"}
	derived, err := s.CreateUserHome(user, t.TempDir(), true)
	if err != nil {
		t.Fatalf("explicit root scope is admin intent, got %v", err)
	}
	if derived {
		t.Error("an explicit scope must not be reported as derived")
	}
	if user.Scope != "/" {
		t.Errorf("explicit scope should be preserved, got %q", user.Scope)
	}
}
