package share

import (
	"time"

	fberrors "github.com/filebrowser/filebrowser/v2/errors"
)

// StorageBackend is the interface to implement for a share storage.
type StorageBackend interface {
	All() ([]*Link, error)
	FindByUserID(id uint) ([]*Link, error)
	GetByHash(hash string) (*Link, error)
	GetPermanent(path string, id uint) (*Link, error)
	Gets(path string, id uint) ([]*Link, error)
	Save(s *Link) error
	Delete(hash string) error
	DeleteWithPathPrefix(path string, userID uint) error
	// DeleteByPathPrefix removes every share at or below path, regardless of
	// owner. It exists for filesystem events (delete, rename), where the path
	// itself disappeared or changed and every link pointing at it is invalid.
	// The share-management API must keep using DeleteWithPathPrefix so one user
	// cannot delete another user's links.
	DeleteByPathPrefix(path string) error
}

// Storage is a storage.
type Storage struct {
	back StorageBackend
}

// NewStorage creates a share links storage from a backend.
func NewStorage(back StorageBackend) *Storage {
	return &Storage{back: back}
}

// All wraps a StorageBackend.All.
func (s *Storage) All() ([]*Link, error) {
	return s.listWithExpiryPurge(s.back.All)
}

// FindByUserID wraps a StorageBackend.FindByUserID.
func (s *Storage) FindByUserID(id uint) ([]*Link, error) {
	return s.listWithExpiryPurge(func() ([]*Link, error) {
		return s.back.FindByUserID(id)
	})
}

// GetByHash wraps a StorageBackend.GetByHash.
func (s *Storage) GetByHash(hash string) (*Link, error) {
	link, err := s.back.GetByHash(hash)
	if err != nil {
		return nil, err
	}

	if link.Expire != 0 && link.Expire <= time.Now().Unix() {
		if err := s.Delete(link.Hash); err != nil {
			return nil, err
		}
		return nil, fberrors.ErrNotExist
	}

	return link, nil
}

// GetPermanent wraps a StorageBackend.GetPermanent
func (s *Storage) GetPermanent(path string, id uint) (*Link, error) {
	return s.back.GetPermanent(path, id)
}

// Gets wraps a StorageBackend.Gets
func (s *Storage) Gets(path string, id uint) ([]*Link, error) {
	return s.listWithExpiryPurge(func() ([]*Link, error) {
		return s.back.Gets(path, id)
	})
}

// listWithExpiryPurge loads a link list, deletes the expired entries, and
// returns only the live ones. Expired links are collected into a fresh slice:
// removing elements while ranging would skip the entry after every deletion
// and over-report expired links as valid.
func (s *Storage) listWithExpiryPurge(load func() ([]*Link, error)) ([]*Link, error) {
	links, err := load()
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	live := make([]*Link, 0, len(links))
	for _, link := range links {
		if link.Expire != 0 && link.Expire <= now {
			if err := s.Delete(link.Hash); err != nil {
				return nil, err
			}
			continue
		}
		live = append(live, link)
	}

	return live, nil
}

// Save wraps a StorageBackend.Save
func (s *Storage) Save(l *Link) error {
	return s.back.Save(l)
}

// Delete wraps a StorageBackend.Delete
func (s *Storage) Delete(hash string) error {
	return s.back.Delete(hash)
}

func (s *Storage) DeleteWithPathPrefix(path string, userID uint) error {
	return s.back.DeleteWithPathPrefix(path, userID)
}

// DeleteByPathPrefix removes every share at or below path, regardless of owner.
// It is used for filesystem events, never by the share-management API.
func (s *Storage) DeleteByPathPrefix(path string) error {
	return s.back.DeleteByPathPrefix(path)
}
