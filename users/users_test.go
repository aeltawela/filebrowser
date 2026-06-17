package users

import (
	"errors"
	"strings"
	"testing"

	fberrors "github.com/filebrowser/filebrowser/v2/errors"
)

func TestUserCleanBookmarks(t *testing.T) {
	t.Run("nil becomes empty", func(t *testing.T) {
		user := &User{}
		if err := user.Clean("", "Bookmarks"); err != nil {
			t.Fatalf("Clean() error = %v", err)
		}
		if user.Bookmarks == nil {
			t.Fatal("expected bookmarks to be initialized")
		}
		if len(user.Bookmarks) != 0 {
			t.Fatalf("expected no bookmarks, got %v", user.Bookmarks)
		}
	})

	t.Run("normalizes and dedupes paths", func(t *testing.T) {
		user := &User{
			Bookmarks: []Bookmark{
				{Path: "/docs/", IsDir: true},
				{Path: "/docs", IsDir: false},
				{Path: "/", IsDir: true},
				{Path: "/reports/./2026", IsDir: true},
			},
		}

		if err := user.Clean("", "Bookmarks"); err != nil {
			t.Fatalf("Clean() error = %v", err)
		}

		want := []Bookmark{
			{Path: "/docs", IsDir: true},
			{Path: "/", IsDir: true},
			{Path: "/reports/2026", IsDir: true},
		}
		if len(user.Bookmarks) != len(want) {
			t.Fatalf("expected %d bookmarks, got %d: %v", len(want), len(user.Bookmarks), user.Bookmarks)
		}
		for i := range want {
			if user.Bookmarks[i] != want[i] {
				t.Fatalf("bookmark %d: expected %#v, got %#v", i, want[i], user.Bookmarks[i])
			}
		}
	})
}

func TestUserCleanBookmarksRejectsInvalidPayload(t *testing.T) {
	tests := map[string][]Bookmark{
		"empty path": {
			{Path: "", IsDir: true},
		},
		"relative path": {
			{Path: "docs", IsDir: true},
		},
		"parent segment": {
			{Path: "/../docs", IsDir: true},
		},
		"too many bookmarks": func() []Bookmark {
			bookmarks := make([]Bookmark, MaxBookmarks+1)
			for i := range bookmarks {
				bookmarks[i] = Bookmark{Path: "/docs", IsDir: true}
			}
			return bookmarks
		}(),
		"path too long": {
			{Path: "/" + strings.Repeat("a", MaxBookmarkPathLength+1), IsDir: false},
		},
	}

	for name, bookmarks := range tests {
		name, bookmarks := name, bookmarks
		t.Run(name, func(t *testing.T) {
			user := &User{Bookmarks: bookmarks}
			err := user.Clean("", "Bookmarks")
			if !errors.Is(err, fberrors.ErrInvalidRequestParams) {
				t.Fatalf("expected ErrInvalidRequestParams, got %v", err)
			}
		})
	}
}

func TestStorageUpdateBookmarks(t *testing.T) {
	backend := &memoryUsersBackend{}
	store := NewStorage(backend)

	user := &User{
		ID: 1,
		Bookmarks: []Bookmark{
			{Path: "/docs/", IsDir: true},
			{Path: "/docs/readme.txt", IsDir: false},
		},
	}

	if err := store.Update(user, "Bookmarks"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if store.LastUpdate(1) == 0 {
		t.Fatal("expected LastUpdate to be set")
	}

	want := []Bookmark{
		{Path: "/docs", IsDir: true},
		{Path: "/docs/readme.txt", IsDir: false},
	}
	if len(backend.user.Bookmarks) != len(want) {
		t.Fatalf("expected %d bookmarks, got %d", len(want), len(backend.user.Bookmarks))
	}
	for i := range want {
		if backend.user.Bookmarks[i] != want[i] {
			t.Fatalf("bookmark %d: expected %#v, got %#v", i, want[i], backend.user.Bookmarks[i])
		}
	}
}

func TestStorageFullUpdatePreservesBookmarks(t *testing.T) {
	backend := &memoryUsersBackend{}
	store := NewStorage(backend)

	user := &User{
		ID:       1,
		Username: "alice",
		Password: "secret",
		Bookmarks: []Bookmark{
			{Path: "/docs", IsDir: true},
		},
	}

	if err := store.Update(user); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	if len(backend.user.Bookmarks) != 1 || backend.user.Bookmarks[0] != user.Bookmarks[0] {
		t.Fatalf("expected bookmarks to be preserved, got %#v", backend.user.Bookmarks)
	}
}

type memoryUsersBackend struct {
	user *User
}

func (m *memoryUsersBackend) GetBy(interface{}) (*User, error) { return m.user, nil }
func (m *memoryUsersBackend) Gets() ([]*User, error)           { return []*User{m.user}, nil }
func (m *memoryUsersBackend) Save(user *User) error {
	m.user = cloneUser(user)
	return nil
}
func (m *memoryUsersBackend) Update(user *User, _ ...string) error {
	m.user = cloneUser(user)
	return nil
}
func (m *memoryUsersBackend) DeleteByID(uint) error         { return nil }
func (m *memoryUsersBackend) DeleteByUsername(string) error { return nil }
func (m *memoryUsersBackend) CountAdmins() (int, error)     { return 0, nil }

func cloneUser(user *User) *User {
	clone := *user
	clone.Bookmarks = append([]Bookmark(nil), user.Bookmarks...)
	return &clone
}
