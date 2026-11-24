package auth

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	fberrors "github.com/filebrowser/filebrowser/v2/errors"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/users"
)

// writeHookScript writes a POSIX shell script to a temp file and returns its
// path, marking it executable.
func writeHookScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatalf("failed to write hook script: %v", err)
	}
	return path
}

// TestRunCommandNoCredentialInjection ensures that attacker-controlled
// credentials submitted at the unauthenticated login endpoint cannot be
// injected into the hook command string. Credentials must only ever reach the
// hook through the USERNAME/PASSWORD environment variables, never via string
// substitution into the command itself (CWE-78/CWE-88).
func TestRunCommandNoCredentialInjection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell")
	}

	marker := filepath.Join(t.TempDir(), "pwned")

	// The hook simply blocks. If the credential were ever interpolated into the
	// command string and evaluated by a shell, the embedded `touch` would
	// create the marker file.
	script := writeHookScript(t, "echo hook.action=block\n")

	a := &HookAuth{
		Command: script,
		Cred: hookCred{
			Username: `"; touch ` + marker + `; #`,
			Password: `$(touch ` + marker + `)`,
		},
	}

	action, err := a.RunCommand()
	if err != nil {
		t.Fatalf("RunCommand returned error: %v", err)
	}
	if action != "block" {
		t.Fatalf("expected action %q, got %q", "block", action)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("credential injection executed: marker file %q was created", marker)
	}
}

// TestRunCommandReceivesCredentialsViaEnv verifies the supported contract: the
// hook receives credentials through the USERNAME and PASSWORD environment
// variables.
func TestRunCommandReceivesCredentialsViaEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell")
	}

	script := writeHookScript(t, `if [ "$USERNAME" = alice ] && [ "$PASSWORD" = secret ]; then
  echo hook.action=auth
else
  echo hook.action=block
fi
`)

	a := &HookAuth{
		Command: script,
		Cred: hookCred{
			Username: "alice",
			Password: "secret",
		},
	}

	action, err := a.RunCommand()
	if err != nil {
		t.Fatalf("RunCommand returned error: %v", err)
	}
	if action != "auth" {
		t.Fatalf("expected action %q, got %q", "auth", action)
	}
}

// newHookAuth builds a HookAuth for a freshly provisioned user with the given
// parsed hook fields (hook.action=auth is added automatically).
func newHookAuth(store *mockUserStore, s *settings.Settings, srv *settings.Server, username string, fields map[string]string) *HookAuth {
	fields["hook.action"] = "auth"
	return &HookAuth{
		Users:    store,
		Settings: s,
		Server:   srv,
		Cred:     hookCred{Username: username, Password: "a-strong-password"},
		Fields:   hookFields{Values: fields},
	}
}

// With CreateUserDir enabled and no explicit scope from the hook, a provisioned
// hook user must receive its own home directory rather than the server root.
func TestHookSaveUserCreateUserDirIsolatesScope(t *testing.T) {
	t.Parallel()

	store := &mockUserStore{users: make(map[string]*users.User)}
	srv := &settings.Server{Root: t.TempDir()}
	s := &settings.Settings{
		Key:              []byte("key"),
		CreateUserDir:    true,
		UserHomeBasePath: "/users",
		Defaults: settings.UserDefaults{
			Scope: ".",
			Perm:  users.Permissions{Create: true},
		},
	}

	u, err := newHookAuth(store, s, srv, "alice", map[string]string{}).SaveUser()
	if err != nil {
		t.Fatalf("SaveUser error: %v", err)
	}
	if u.Scope != "/users/alice" {
		t.Errorf("hook user without explicit scope: expected /users/alice, got %q", u.Scope)
	}
}

// A scope explicitly returned by the hook takes precedence over the automatic
// per-user home directory derivation.
func TestHookSaveUserRespectsExplicitScope(t *testing.T) {
	t.Parallel()

	store := &mockUserStore{users: make(map[string]*users.User)}
	srv := &settings.Server{Root: t.TempDir()}
	s := &settings.Settings{
		Key:              []byte("key"),
		CreateUserDir:    true,
		UserHomeBasePath: "/users",
		Defaults: settings.UserDefaults{
			Scope: ".",
			Perm:  users.Permissions{Create: true},
		},
	}

	u, err := newHookAuth(store, s, srv, "teamlead", map[string]string{"user.scope": "/shared/team"}).SaveUser()
	if err != nil {
		t.Fatalf("SaveUser error: %v", err)
	}
	if u.Scope != "/shared/team" {
		t.Errorf("explicit hook scope should win, got %q", u.Scope)
	}
}

// A hook-provisioned account must not silently inherit shell-equivalent
// rights from permissive defaults: Admin and Execute (and the command list)
// require an explicit hook statement, mirroring signup and proxy
// auto-provisioning.
func TestHookProvisionStripsDefaultAdmin(t *testing.T) {
	back := &hookTestBackend{users: make(map[uint]*users.User)}
	store := users.NewStorage(back)
	serverRoot := t.TempDir()

	newHook := func(outputs map[string]string) *HookAuth {
		values := map[string]string{"hook.action": "auth"}
		for k, v := range outputs {
			values[k] = v
		}
		return &HookAuth{
			Users: store,
			Settings: &settings.Settings{
				MinimumPasswordLength: settings.DefaultMinimumPasswordLength,
				Defaults: settings.UserDefaults{
					Scope:    "/team",
					Perm:     users.Permissions{Admin: true, Execute: true},
					Commands: []string{"danger"},
				},
			},
			Server: &settings.Server{Root: serverRoot},
			Cred:   hookCred{Username: "hookuser", Password: "HookPass123!"},
			Fields: hookFields{Values: values},
		}
	}

	u, err := newHook(nil).SaveUser()
	if err != nil {
		t.Fatal(err)
	}
	if u.Perm.Admin || u.Perm.Execute {
		t.Fatalf("VULNERABLE: hook-provisioned user inherited admin=%t execute=%t from defaults", u.Perm.Admin, u.Perm.Execute)
	}
	if len(u.Commands) != 0 {
		t.Fatalf("hook-provisioned user inherited commands %q from defaults", u.Commands)
	}

	explicit, err := newHook(map[string]string{"user.perm.admin": "true"}).SaveUser()
	if err != nil {
		// Second provisioning updates the existing user; the explicit grant
		// must survive.
		t.Fatal(err)
	}
	if !explicit.Perm.Admin {
		t.Fatal("explicit user.perm.admin=true was stripped")
	}
}

type hookTestBackend struct {
	users map[uint]*users.User
	next  uint
}

func (b *hookTestBackend) GetBy(i interface{}) (*users.User, error) {
	id, ok := i.(uint)
	if !ok {
		if name, ok := i.(string); ok {
			for _, u := range b.users {
				if u.Username == name {
					cp := *u
					return &cp, nil
				}
			}
		}
		return nil, fberrors.ErrNotExist
	}
	u, ok := b.users[id]
	if !ok {
		return nil, fberrors.ErrNotExist
	}
	cp := *u
	return &cp, nil
}
func (b *hookTestBackend) GetByScope(string) (*users.User, error) { return nil, fberrors.ErrNotExist }
func (b *hookTestBackend) Gets() ([]*users.User, error) {
	out := make([]*users.User, 0, len(b.users))
	for _, u := range b.users {
		cp := *u
		out = append(out, &cp)
	}
	return out, nil
}
func (b *hookTestBackend) Save(u *users.User) error {
	if u.ID == 0 {
		b.next++
		u.ID = b.next
	}
	cp := *u
	b.users[u.ID] = &cp
	return nil
}
func (b *hookTestBackend) Update(u *users.User, _ ...string) error { return b.Save(u) }
func (b *hookTestBackend) DeleteByID(uint) error                   { return nil }
func (b *hookTestBackend) DeleteByUsername(string) error           { return nil }
func (b *hookTestBackend) CountAdmins() (int, error)               { return 0, nil }
func (b *hookTestBackend) IncrementTokenVersion(id uint) error {
	u, ok := b.users[id]
	if !ok {
		return fberrors.ErrNotExist
	}
	u.TokenVersion++
	return nil
}
