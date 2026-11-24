package bolt

import (
	"errors"
	"strings"

	"github.com/asdine/storm/v3"
	"github.com/asdine/storm/v3/q"

	fberrors "github.com/filebrowser/filebrowser/v2/errors"
	"github.com/filebrowser/filebrowser/v2/share"
)

type shareBackend struct {
	db *storm.DB
}

func (s shareBackend) All() ([]*share.Link, error) {
	var v []*share.Link
	err := s.db.All(&v)
	if errors.Is(err, storm.ErrNotFound) {
		return v, fberrors.ErrNotExist
	}

	return v, err
}

func (s shareBackend) FindByUserID(id uint) ([]*share.Link, error) {
	var v []*share.Link
	err := s.db.Select(q.Eq("UserID", id)).Find(&v)
	if errors.Is(err, storm.ErrNotFound) {
		return v, fberrors.ErrNotExist
	}

	return v, err
}

func (s shareBackend) GetByHash(hash string) (*share.Link, error) {
	var v share.Link
	err := s.db.One("Hash", hash, &v)
	if errors.Is(err, storm.ErrNotFound) {
		return nil, fberrors.ErrNotExist
	}

	return &v, err
}

func (s shareBackend) GetPermanent(path string, id uint) (*share.Link, error) {
	var v share.Link
	err := s.db.Select(q.Eq("Path", path), q.Eq("Expire", 0), q.Eq("UserID", id)).First(&v)
	if errors.Is(err, storm.ErrNotFound) {
		return nil, fberrors.ErrNotExist
	}

	return &v, err
}

func (s shareBackend) Gets(path string, id uint) ([]*share.Link, error) {
	var v []*share.Link
	err := s.db.Select(q.Eq("Path", path), q.Eq("UserID", id)).Find(&v)
	if errors.Is(err, storm.ErrNotFound) {
		return v, fberrors.ErrNotExist
	}

	return v, err
}

func (s shareBackend) Save(l *share.Link) error {
	return s.db.Save(l)
}

func (s shareBackend) Delete(hash string) error {
	err := s.db.DeleteStruct(&share.Link{Hash: hash})
	if errors.Is(err, storm.ErrNotFound) {
		return nil
	}
	return err
}

// DeleteWithPathPrefix removes the links owned by userID at or below pathPrefix.
// The owner filter keeps the share-management API from touching another user's
// links; filesystem events use DeleteByPathPrefix instead.
func (s shareBackend) DeleteWithPathPrefix(pathPrefix string, userID uint) error {
	return s.deleteByPathPrefix(pathPrefix, &userID)
}

// DeleteByPathPrefix removes every link at or below pathPrefix, regardless of
// owner. Callers must only use it after the filesystem path itself was deleted
// or moved, where every link pointing at it is invalid.
func (s shareBackend) DeleteByPathPrefix(pathPrefix string) error {
	return s.deleteByPathPrefix(pathPrefix, nil)
}

func (s shareBackend) deleteByPathPrefix(pathPrefix string, userID *uint) error {
	// Share paths are stored without a trailing slash
	prefix := strings.TrimRight(pathPrefix, "/")

	var links []share.Link
	if err := s.db.Prefix("Path", prefix, &links); err != nil {
		if errors.Is(err, storm.ErrNotFound) {
			return nil
		}
		return err
	}

	var err error
	for _, link := range links {
		if userID != nil && link.UserID != *userID {
			continue
		}

		if link.Path != prefix && !strings.HasPrefix(link.Path, prefix+"/") {
			continue
		}

		err = errors.Join(err, s.db.DeleteStruct(&share.Link{Hash: link.Hash}))
	}
	return err
}
