package users

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	fberrors "github.com/filebrowser/filebrowser/v2/errors"
	"github.com/filebrowser/filebrowser/v2/rules"
)

// Interface is implemented by storage
var _ Store = &Storage{}

// slowBackend is a minimal StorageBackend whose scope lookup is deliberately
// slow, so that an unsynchronized check-then-save reliably interleaves.
type slowBackend struct {
	mu    sync.Mutex
	users []*User
	delay time.Duration
}

func (b *slowBackend) GetByScope(scope string) (*User, error) {
	time.Sleep(b.delay)

	b.mu.Lock()
	defer b.mu.Unlock()
	for _, u := range b.users {
		if strings.EqualFold(u.Scope, scope) {
			return u, nil
		}
	}
	return nil, fberrors.ErrNotExist
}

func (b *slowBackend) Save(u *User) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.users = append(b.users, u)
	return nil
}

func (b *slowBackend) GetBy(interface{}) (*User, error) { return nil, fberrors.ErrNotExist }
func (b *slowBackend) Gets() ([]*User, error)           { return b.users, nil }
func (b *slowBackend) Update(*User, ...string) error    { return nil }
func (b *slowBackend) DeleteByID(uint) error            { return nil }
func (b *slowBackend) DeleteByUsername(string) error    { return nil }
func (b *slowBackend) CountAdmins() (int, error)        { return 0, nil }

// Two usernames that normalize to the same home directory must not both be
// provisioned, even when their requests are handled concurrently: checking the
// scope and saving the user has to be a single atomic step.
func TestSaveProvisionedRejectsConcurrentScopeCollision(t *testing.T) {
	back := &slowBackend{delay: 50 * time.Millisecond}
	store := NewStorage(back)

	errs := make([]error, 2)
	var wg, ready sync.WaitGroup
	ready.Add(2)
	for i, username := range []string{"teamone-x", "teamone/x"} {
		wg.Add(1)
		go func(i int, username string) {
			defer wg.Done()
			// Only start once both goroutines are actually running, so that an
			// unsynchronized implementation is guaranteed to interleave rather
			// than to pass by scheduling luck.
			ready.Done()
			ready.Wait()
			errs[i] = store.SaveProvisioned(&User{
				Username: username,
				Password: "pw",
				Scope:    "/users/teamone-x",
			}, true)
		}(i, username)
	}
	wg.Wait()

	saved := 0
	for _, err := range errs {
		switch {
		case err == nil:
			saved++
		case !errors.Is(err, fberrors.ErrExist):
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if saved != 1 {
		t.Fatalf("VULNERABLE: %d users provisioned into /users/teamone-x, want 1", saved)
	}
	if len(back.users) != 1 {
		t.Fatalf("%d users stored, want 1", len(back.users))
	}
}

// A scope that was not derived from the username (explicitly configured by an
// administrator or returned by an auth hook) may legitimately be shared, so it
// must not be subject to the collision check.
func TestSaveProvisionedAllowsSharedExplicitScope(t *testing.T) {
	back := &slowBackend{}
	store := NewStorage(back)

	for _, username := range []string{"alice", "bob"} {
		if err := store.SaveProvisioned(&User{
			Username: username,
			Password: "pw",
			Scope:    "/shared/team",
		}, false); err != nil {
			t.Fatalf("saving %q with an explicit shared scope: %v", username, err)
		}
	}

	if len(back.users) != 2 {
		t.Fatalf("%d users stored, want 2", len(back.users))
	}
}

func (b *slowBackend) IncrementTokenVersion(uint) error { return nil }

// versionBackend is a minimal backend that keeps users in memory so that token
// version bumps can be observed without a real database.
type versionBackend struct {
	users map[uint]*User
}

func (b *versionBackend) GetBy(i interface{}) (*User, error) {
	id, ok := i.(uint)
	if !ok {
		return nil, fberrors.ErrInvalidDataType
	}
	u, ok := b.users[id]
	if !ok {
		return nil, fberrors.ErrNotExist
	}
	return u, nil
}

func (b *versionBackend) GetByScope(string) (*User, error) { return nil, fberrors.ErrNotExist }

func (b *versionBackend) Gets() ([]*User, error) {
	out := make([]*User, 0, len(b.users))
	for _, u := range b.users {
		out = append(out, u)
	}
	return out, nil
}

func (b *versionBackend) Save(u *User) error {
	if u.ID == 0 {
		u.ID = uint(len(b.users) + 1)
	}
	saved := *u
	b.users[u.ID] = &saved
	return nil
}

func (b *versionBackend) Update(u *User, _ ...string) error {
	saved := *u
	b.users[u.ID] = &saved
	return nil
}

func (b *versionBackend) DeleteByID(uint) error { return nil }

func (b *versionBackend) DeleteByUsername(string) error { return nil }

func (b *versionBackend) CountAdmins() (int, error) { return 0, nil }

func (b *versionBackend) IncrementTokenVersion(id uint) error {
	u, ok := b.users[id]
	if !ok {
		return fberrors.ErrNotExist
	}
	u.TokenVersion++
	return nil
}

// A regex rule whose pattern text changes authorizes a different set of
// paths, so it must revoke even when Path/Allow/Regex look identical.
func TestRuleRegexpChangeRevokesToken(t *testing.T) {
	back := &versionBackend{users: make(map[uint]*User)}
	store := NewStorage(back)
	u := &User{
		Username: "alice",
		Password: "pw",
		Scope:    "/alice",
		Rules:    []rules.Rule{{Regex: true, Allow: false, Path: "/x", Regexp: &rules.Regexp{Raw: "secret.*"}}},
	}
	if err := store.Save(u); err != nil {
		t.Fatal(err)
	}

	changed := *u
	changed.Rules = []rules.Rule{{Regex: true, Allow: false, Path: "/x", Regexp: &rules.Regexp{Raw: "other.*"}}}
	if err := store.Update(&changed); err != nil {
		t.Fatal(err)
	}
	if got := back.users[u.ID].TokenVersion; got != 1 {
		t.Fatalf("regex pattern change left the epoch at %d, want 1", got)
	}

	// Identical rules (including pattern text) must not revoke. Build from
	// the post-update object, whose epoch was already refreshed by Update.
	same := changed
	same.Locale = "de"
	if err := store.Update(&same); err != nil {
		t.Fatal(err)
	}
	if got := back.users[u.ID].TokenVersion; got != 1 {
		t.Fatalf("identical rules bumped the epoch to %d, want 1", got)
	}
}

// Any caller of Update (HTTP, hook or CLI) must revoke tokens when a sensitive
// field actually changes; unrelated updates must leave sessions intact.
func TestUpdateRevokesOnSensitiveChanges(t *testing.T) {
	back := &versionBackend{users: make(map[uint]*User)}
	store := NewStorage(back)
	u := &User{Username: "alice", Password: "pw", Scope: "/alice"}
	if err := store.Save(u); err != nil {
		t.Fatal(err)
	}

	u.Locale = "de"
	if err := store.Update(u); err != nil {
		t.Fatal(err)
	}
	if got := back.users[u.ID].TokenVersion; got != 0 {
		t.Fatalf("non-sensitive update bumped the version to %d", got)
	}

	u.Perm = Permissions{Admin: true}
	if err := store.Update(u); err != nil {
		t.Fatal(err)
	}
	if got := back.users[u.ID].TokenVersion; got != 1 {
		t.Fatalf("permission change bumped the version to %d, want 1", got)
	}
}

// A bump must be persisted monotonically so that tokens minted before it are
// rejected on every subsequent request.
func TestIncrementTokenVersion(t *testing.T) {
	back := &versionBackend{users: make(map[uint]*User)}
	store := NewStorage(back)
	u := &User{Username: "alice", Password: "pw"}
	if err := store.Save(u); err != nil {
		t.Fatal(err)
	}

	for want := uint64(1); want <= 3; want++ {
		if err := store.IncrementTokenVersion(u.ID); err != nil {
			t.Fatal(err)
		}
		got, err := store.Get("", false, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.TokenVersion != want {
			t.Fatalf("after %d bumps version = %d, want %d", want, got.TokenVersion, want)
		}
	}
}

// A full save of a rebuilt record (hook auth reconstructs the user on every
// login) must preserve the live session epoch instead of resetting it to zero,
// or logout and password-reset revocation would be undone by the next login.
// When the save does change a sensitive field, the epoch must advance and the
// caller's object must carry the new epoch so tokens minted from it work.
func TestFullSavePreservesTokenVersion(t *testing.T) {
	back := &versionBackend{users: make(map[uint]*User)}
	store := NewStorage(back)
	u := &User{Username: "alice", Password: "pw", Scope: "/alice"}
	if err := store.Save(u); err != nil {
		t.Fatal(err)
	}
	if err := store.IncrementTokenVersion(u.ID); err != nil {
		t.Fatal(err)
	}

	// Rebuilt the way HookAuth.GetUser builds it: same security fields, an
	// innocuous extra field, but a zero TokenVersion.
	rebuilt := &User{ID: u.ID, Username: "alice", Password: "pw", Scope: "/alice", Locale: "en"}
	if err := store.Update(rebuilt); err != nil {
		t.Fatal(err)
	}
	if got := back.users[u.ID].TokenVersion; got != 1 {
		t.Fatalf("VULNERABLE: full save reset the epoch to %d; revoked tokens are valid again", got)
	}
	if rebuilt.TokenVersion != 1 {
		t.Fatalf("caller object carries a stale epoch %d; freshly minted tokens would be rejected", rebuilt.TokenVersion)
	}

	rebuilt.Scope = "/other"
	if err := store.Update(rebuilt); err != nil {
		t.Fatal(err)
	}
	if got := back.users[u.ID].TokenVersion; got != 2 {
		t.Fatalf("scope change left the epoch at %d, want 2", got)
	}
	if rebuilt.TokenVersion != 2 {
		t.Fatalf("caller object carries epoch %d, want 2", rebuilt.TokenVersion)
	}
}

// Saving over an existing record (users import --overwrite) must preserve the
// live epoch, and must still revoke when the imported record changes a
// sensitive field — including a forged or stale TokenVersion in the file.
func TestSaveOverExistingPreservesEpochAndRevokes(t *testing.T) {
	back := &versionBackend{users: make(map[uint]*User)}
	store := NewStorage(back)
	u := &User{Username: "alice", Password: "pw", Scope: "/alice"}
	if err := store.Save(u); err != nil {
		t.Fatal(err)
	}
	if err := store.IncrementTokenVersion(u.ID); err != nil {
		t.Fatal(err)
	}

	imported := &User{ID: u.ID, Username: "alice", Password: "pw", Scope: "/alice", TokenVersion: 0}
	if err := store.Save(imported); err != nil {
		t.Fatal(err)
	}
	if got := back.users[u.ID].TokenVersion; got != 1 {
		t.Fatalf("VULNERABLE: import reset the epoch to %d", got)
	}
	if imported.TokenVersion != 1 {
		t.Fatalf("caller object carries epoch %d, want 1", imported.TokenVersion)
	}

	imported.Password = "rotated"
	if err := store.Save(imported); err != nil {
		t.Fatal(err)
	}
	if got := back.users[u.ID].TokenVersion; got != 2 {
		t.Fatalf("imported password change left the epoch at %d, want 2", got)
	}
}

// Concurrent full saves (hook auth re-saves the record on every login) must
// not move the session epoch backward over concurrent revocations: the
// read-modify-write cycles are serialized per user, so every bump survives.
func TestConcurrentSavesAndRevocationsKeepEpochMonotonic(t *testing.T) {
	back := &versionBackend{users: make(map[uint]*User)}
	store := NewStorage(back)
	u := &User{Username: "alice", Password: "pw", Scope: "/alice"}
	if err := store.Save(u); err != nil {
		t.Fatal(err)
	}

	const revocations = 8
	var wg sync.WaitGroup
	for i := 0; i < revocations; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			// Hook-style full save of a rebuilt record: no sensitive change,
			// so it must preserve the live epoch, never rewind it.
			rebuilt := &User{ID: u.ID, Username: "alice", Password: "pw", Scope: "/alice", Locale: "en"}
			if err := store.Update(rebuilt); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if err := store.IncrementTokenVersion(u.ID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if got := back.users[u.ID].TokenVersion; got != revocations {
		t.Fatalf("VULNERABLE: epoch = %d, want %d; a concurrent full save undid a revocation", got, revocations)
	}
}
