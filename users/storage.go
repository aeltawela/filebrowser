package users

import (
	"errors"
	"strings"
	"sync"
	"time"

	fberrors "github.com/filebrowser/filebrowser/v2/errors"
	"github.com/filebrowser/filebrowser/v2/rules"
)

// StorageBackend is the interface to implement for a users storage.
type StorageBackend interface {
	GetBy(interface{}) (*User, error)
	GetByScope(scope string) (*User, error)
	Gets() ([]*User, error)
	Save(u *User) error
	Update(u *User, fields ...string) error
	DeleteByID(uint) error
	DeleteByUsername(string) error
	CountAdmins() (int, error)
	// IncrementTokenVersion atomically advances a user's session epoch.
	IncrementTokenVersion(uint) error
}

type Store interface {
	Get(baseScope string, followExternalSymlinks bool, id interface{}) (user *User, err error)
	GetByScope(scope string) (*User, error)
	Gets(baseScope string, followExternalSymlinks bool) ([]*User, error)
	Update(user *User, fields ...string) error
	Save(user *User) error
	SaveProvisioned(user *User, derivedScope bool) error
	Delete(id interface{}) error
	LastUpdate(id uint) int64
	IncrementTokenVersion(id uint) error
}

// Storage is a users storage.
type Storage struct {
	back    StorageBackend
	updated map[uint]int64
	mux     sync.RWMutex

	// provision serializes the scope-collision check and the save of newly
	// provisioned users, which must not interleave. See SaveProvisioned.
	provision sync.Mutex

	// epochLocks serializes the read-modify-write cycles on a user record
	// (epoch preserve/bump in Update, Save and IncrementTokenVersion). The
	// backend transactions are individually atomic, but the sequences span
	// several of them: without this, a full save that read the epoch before
	// a concurrent revocation could write the stale epoch back and undo the
	// revocation. Striped and fixed-size, so there is no per-user entry to
	// clean up; user writes are rare, so sharing a stripe is harmless.
	epochLocks [32]sync.Mutex
}

// lockUser serializes mutations of one user's record. The returned function
// releases the lock.
func (s *Storage) lockUser(id uint) func() {
	l := &s.epochLocks[id%uint(len(s.epochLocks))]
	l.Lock()
	return l.Unlock
}

// NewStorage creates a users storage from a backend.
func NewStorage(back StorageBackend) *Storage {
	return &Storage{
		back:    back,
		updated: map[uint]int64{},
	}
}

// Get allows you to get a user by its name or username. The provided
// id must be a string for username lookup or a uint for id lookup. If id
// is neither, a ErrInvalidDataType will be returned.
func (s *Storage) Get(baseScope string, followExternalSymlinks bool, id interface{}) (user *User, err error) {
	user, err = s.back.GetBy(id)
	if err != nil {
		return
	}
	if err := user.Clean(baseScope, followExternalSymlinks); err != nil {
		return nil, err
	}
	return
}

// GetByScope returns the first user whose scope matches the given one, or
// ErrNotExist if none does. The user is returned as stored, without setting up
// its filesystem, as it is meant for existence checks rather than serving.
func (s *Storage) GetByScope(scope string) (*User, error) {
	return s.back.GetByScope(scope)
}

// Gets gets a list of all users.
func (s *Storage) Gets(baseScope string, followExternalSymlinks bool) ([]*User, error) {
	users, err := s.back.Gets()
	if err != nil {
		return nil, err
	}

	for _, user := range users {
		if err := user.Clean(baseScope, followExternalSymlinks); err != nil {
			return nil, err
		}
	}

	return users, err
}

// Update updates a user in the database.
//
// Security-sensitive changes advance the persisted session epoch here, at the
// storage choke point, so every caller (HTTP handlers, hook auth and the CLI)
// revokes old tokens consistently. A caller cannot opt out by choosing a
// different update path.
//
// The sensitive set is every field that changes what the user may do or how
// they authenticate: Password, Perm (capabilities), LockPassword (account
// lock), Scope and Rules (authorized paths). Display preferences (locale,
// theme, sorting, bookmarks) never revoke: changing them must not kick the
// user out on every device. Identity renames (Username) do not revoke either:
// tokens bind the immutable user ID, so a rename alters nothing the token
// authorizes.
//
// TokenVersion is server-owned: it is always preserved from the stored record,
// never taken from the caller's object. Callers such as hook auth rebuild the
// user from scratch on every login and would otherwise reset the epoch to zero,
// undoing logout and password-reset revocation.
func (s *Storage) Update(user *User, fields ...string) error {
	err := user.Clean("", false, fields...)
	if err != nil {
		return err
	}

	// Serialize with concurrent mutations of the same record so the epoch
	// read below cannot go stale before the write and the bump.
	unlock := s.lockUser(user.ID)
	defer unlock()

	revoke := false
	old, getErr := s.back.GetBy(user.ID)
	if getErr != nil && !errors.Is(getErr, fberrors.ErrNotExist) {
		// Fail closed: without the stored record there is no epoch to
		// preserve and no baseline to compare, so persisting the caller's
		// object could reset revocation. A missing record (NotExist) simply
		// proceeds to the backend update, which reports the outcome.
		return getErr
	}
	if old != nil {
		// Preserve the live epoch. Without this, a full save of a rebuilt
		// record (hook auth) persists a zero TokenVersion and resurrects
		// revoked tokens.
		user.TokenVersion = old.TokenVersion
		for _, change := range []struct {
			field   string
			changed bool
		}{
			{"Password", old.Password != user.Password},
			{"Perm", old.Perm != user.Perm},
			{"LockPassword", old.LockPassword != user.LockPassword},
			{"Scope", old.Scope != user.Scope},
			{"Rules", !equalRules(old.Rules, user.Rules)},
		} {
			if change.changed && fieldUpdated(fields, change.field) {
				revoke = true
				break
			}
		}
	}

	err = s.back.Update(user, fields...)
	if err != nil {
		return err
	}

	if revoke {
		if err := s.back.IncrementTokenVersion(user.ID); err != nil {
			return err
		}
		// Refresh the caller's object from the store so tokens minted from it
		// (hook-auth login mints immediately after Update) carry the new epoch
		// instead of a stale one that every request would reject.
		if updated, getErr := s.back.GetBy(user.ID); getErr == nil && updated != nil {
			user.TokenVersion = updated.TokenVersion
		}
	}

	s.mux.Lock()
	s.updated[user.ID] = time.Now().Unix()
	s.mux.Unlock()
	return nil
}

// fieldUpdated reports whether a field takes part in this update. An empty
// field list means a full record save.
func fieldUpdated(fields []string, field string) bool {
	if len(fields) == 0 {
		return true
	}
	for _, f := range fields {
		if strings.EqualFold(f, field) || strings.EqualFold(f, "all") {
			return true
		}
	}
	return false
}

// equalRules reports whether two rule sets authorize the same paths. Rules
// carry compiled state, so they are compared field by field instead of with
// reflection over the whole struct.
func equalRules(a, b []rules.Rule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i].Path || a[i].Allow != b[i].Allow || a[i].Regex != b[i].Regex {
			return false
		}
		// Compare the raw pattern text too: two regex rules can share Path
		// and flags while matching different sets. The compiled Regexp is
		// derived from Raw, so Raw equality implies equal matching.
		aRaw, bRaw := "", ""
		if a[i].Regexp != nil {
			aRaw = a[i].Regexp.Raw
		}
		if b[i].Regexp != nil {
			bRaw = b[i].Regexp.Raw
		}
		if aRaw != bRaw {
			return false
		}
	}
	return true
}

// Save saves the user in a storage.
//
// When the record already exists (for example `users import --overwrite`),
// the live session epoch is preserved and sensitive-field changes revoke old
// tokens, exactly as in Update. A full save must not be a way to reset
// revocation or to forge the epoch from an imported file.
func (s *Storage) Save(user *User) error {
	if err := user.Clean("", false); err != nil {
		return err
	}

	// Serialize with concurrent mutations of the same record; see Update.
	unlock := s.lockUser(user.ID)
	defer unlock()

	old, getErr := s.back.GetBy(user.ID)
	if getErr != nil && !errors.Is(getErr, fberrors.ErrNotExist) {
		// Fail closed for the same reason as Update: no stored baseline,
		// no safe overwrite.
		return getErr
	}
	if old != nil {
		sensitive := old.Password != user.Password ||
			old.Perm != user.Perm ||
			old.LockPassword != user.LockPassword ||
			old.Scope != user.Scope ||
			!equalRules(old.Rules, user.Rules)
		user.TokenVersion = old.TokenVersion
		if err := s.back.Save(user); err != nil {
			return err
		}
		if sensitive {
			if err := s.back.IncrementTokenVersion(user.ID); err != nil {
				return err
			}
			if updated, rerr := s.back.GetBy(user.ID); rerr == nil && updated != nil {
				user.TokenVersion = updated.TokenVersion
			}
		}
		return nil
	}

	return s.back.Save(user)
}

// SaveProvisioned saves a user that is being provisioned (via signup, proxy
// auth or hook auth). When its scope was derived from the username, it first
// rejects the save if another user already owns that scope, so that distinct
// usernames cannot silently share one home directory.
//
// The check and the save are held under a single lock. Performing them as two
// independent operations lets two concurrent provisioning requests both observe
// a free scope and both save, leaving two users sharing one home directory.
func (s *Storage) SaveProvisioned(user *User, derivedScope bool) error {
	if !derivedScope {
		return s.Save(user)
	}

	s.provision.Lock()
	defer s.provision.Unlock()

	switch _, err := s.back.GetByScope(user.Scope); {
	case err == nil:
		return fberrors.ErrExist
	case !errors.Is(err, fberrors.ErrNotExist):
		return err
	}

	return s.Save(user)
}

// IncrementTokenVersion revokes every token previously issued to a user. The
// increment is atomic in the backend so that concurrent revocations cannot
// lose an update and accidentally restore an older epoch. It takes the same
// per-user lock as Update and Save so a concurrent full save cannot write a
// stale epoch back over the bump.
func (s *Storage) IncrementTokenVersion(id uint) error {
	unlock := s.lockUser(id)
	defer unlock()

	return s.back.IncrementTokenVersion(id)
}

// Delete allows you to delete a user by its name or username. The provided
// id must be a string for username lookup or a uint for id lookup. If id
// is neither, a ErrInvalidDataType will be returned.
func (s *Storage) Delete(id interface{}) error {
	switch id := id.(type) {
	case string:
		user, err := s.back.GetBy(id)
		if err != nil {
			return err
		}
		if s.IsUniqueAdmin(user) {
			return fberrors.ErrRootUserDeletion
		}

		return s.back.DeleteByUsername(id)
	case uint:
		user, err := s.back.GetBy(id)
		if err != nil {
			return err
		}
		if s.IsUniqueAdmin(user) {
			return fberrors.ErrRootUserDeletion
		}

		return s.back.DeleteByID(id)
	default:
		return fberrors.ErrInvalidDataType
	}
}

// LastUpdate gets the timestamp for the last update of an user.
func (s *Storage) LastUpdate(id uint) int64 {
	s.mux.RLock()
	defer s.mux.RUnlock()
	if val, ok := s.updated[id]; ok {
		return val
	}
	return 0
}

func (s *Storage) IsUniqueAdmin(user *User) bool {
	if !user.Perm.Admin {
		return false
	}

	count, err := s.back.CountAdmins()
	if err != nil {
		return true
	}
	return count <= 1
}
