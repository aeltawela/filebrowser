package users

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	fberrors "github.com/filebrowser/filebrowser/v2/errors"
	"github.com/filebrowser/filebrowser/v2/files"
	"github.com/filebrowser/filebrowser/v2/rules"
	"github.com/spf13/afero"
)

// ViewMode describes a view mode.
type ViewMode string

const (
	ListViewMode   ViewMode = "list"
	MosaicViewMode ViewMode = "mosaic"
)

const (
	MaxBookmarks          = 100
	MaxBookmarkPathLength = 2048
)

// Bookmark describes a saved path for quick access.
type Bookmark struct {
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

// User describes a user.
type User struct {
	ID                    uint            `storm:"id,increment" json:"id"`
	Username              string          `storm:"unique" json:"username"`
	Password              string          `json:"password"`
	Scope                 string          `json:"scope"`
	Locale                string          `json:"locale"`
	LockPassword          bool            `json:"lockPassword"`
	ViewMode              ViewMode        `json:"viewMode"`
	SingleClick           bool            `json:"singleClick"`
	RedirectAfterCopyMove bool            `json:"redirectAfterCopyMove"`
	Perm                  Permissions     `json:"perm"`
	Commands              []string        `json:"commands"`
	Sorting               files.Sorting   `json:"sorting"`
	Fs                    *files.ScopedFs `json:"-" yaml:"-"`
	Rules                 []rules.Rule    `json:"rules"`
	HideDotfiles          bool            `json:"hideDotfiles"`
	DateFormat            bool            `json:"dateFormat"`
	AceEditorTheme        string          `json:"aceEditorTheme"`
	Bookmarks             []Bookmark      `json:"bookmarks"`
}

// GetRules implements rules.Provider.
func (u *User) GetRules() []rules.Rule {
	return u.Rules
}

var checkableFields = []string{
	"Username",
	"Password",
	"Scope",
	"ViewMode",
	"Commands",
	"Sorting",
	"Rules",
	"Bookmarks",
}

// Clean cleans up a user and verifies if all its fields
// are alright to be saved.
func (u *User) Clean(baseScope string, fields ...string) error {
	if len(fields) == 0 {
		fields = checkableFields
	}

	for _, field := range fields {
		switch field {
		case "Username":
			if u.Username == "" {
				return fberrors.ErrEmptyUsername
			}
		case "Password":
			if u.Password == "" {
				return fberrors.ErrEmptyPassword
			}
		case "ViewMode":
			if u.ViewMode == "" {
				u.ViewMode = ListViewMode
			}
		case "Commands":
			if u.Commands == nil {
				u.Commands = []string{}
			}
		case "Sorting":
			if u.Sorting.By == "" {
				u.Sorting.By = "name"
			}
		case "Rules":
			if u.Rules == nil {
				u.Rules = []rules.Rule{}
			}
		case "Bookmarks":
			bookmarks, err := cleanBookmarks(u.Bookmarks)
			if err != nil {
				return err
			}
			u.Bookmarks = bookmarks
		}
	}

	if u.Fs == nil {
		scope := u.Scope
		scope = filepath.Join(baseScope, filepath.Join("/", scope))
		u.Fs = files.NewScopedFs(afero.NewOsFs(), scope)
	}

	return nil
}

func cleanBookmarks(bookmarks []Bookmark) ([]Bookmark, error) {
	if bookmarks == nil {
		return []Bookmark{}, nil
	}

	if len(bookmarks) > MaxBookmarks {
		return nil, fmt.Errorf("bookmark limit exceeded: %w", fberrors.ErrInvalidRequestParams)
	}

	cleaned := make([]Bookmark, 0, len(bookmarks))
	seen := make(map[string]struct{}, len(bookmarks))
	for _, bookmark := range bookmarks {
		normalized, err := cleanBookmarkPath(bookmark.Path)
		if err != nil {
			return nil, err
		}

		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}

		cleaned = append(cleaned, Bookmark{
			Path:  normalized,
			IsDir: bookmark.IsDir,
		})
	}

	return cleaned, nil
}

func cleanBookmarkPath(bookmarkPath string) (string, error) {
	if bookmarkPath == "" {
		return "", fmt.Errorf("bookmark path is empty: %w", fberrors.ErrInvalidRequestParams)
	}

	if len(bookmarkPath) > MaxBookmarkPathLength {
		return "", fmt.Errorf("bookmark path is too long: %w", fberrors.ErrInvalidRequestParams)
	}

	if !strings.HasPrefix(bookmarkPath, "/") {
		return "", fmt.Errorf("bookmark path must be absolute: %w", fberrors.ErrInvalidRequestParams)
	}

	for _, segment := range strings.Split(bookmarkPath, "/") {
		if segment == ".." {
			return "", fmt.Errorf("bookmark path contains invalid segment: %w", fberrors.ErrInvalidRequestParams)
		}
	}

	cleaned := path.Clean(bookmarkPath)
	if cleaned == "." {
		cleaned = "/"
	}

	if len(cleaned) > MaxBookmarkPathLength {
		return "", fmt.Errorf("bookmark path is too long: %w", fberrors.ErrInvalidRequestParams)
	}

	return cleaned, nil
}

// FullPath gets the full path for a user's relative path.
func (u *User) FullPath(path string) string {
	return afero.FullBaseFsPath(u.Fs.Base(), path)
}
