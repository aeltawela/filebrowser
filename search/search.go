package search

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/rules"
)

type searchOptions struct {
	CaseSensitive bool
	Conditions    []condition
	Terms         []string
}

// Search searches for a query in a fs.
func Search(ctx context.Context,
	fs afero.Fs, scope, query string, checker rules.Checker, found func(path string, f os.FileInfo) error) error {
	search := parseSearch(query)

	scope = cleanPath(scope)
	index, ok, err := defaultIndexes.get(ctx, fs, scope, checker)
	if err != nil {
		return err
	}
	if ok {
		return index.search(ctx, search, checker, found)
	}

	return walkUnsorted(ctx, fs, scope, func(fPath string, f os.FileInfo, err error) error {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}

		if fPath == scope {
			return nil
		}

		if err != nil {
			return nil
		}

		if !checker.Check(fPath) {
			if f.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if !search.matches(fPath) {
			return nil
		}

		return found(relativePath(scope, fPath), f)
	})
}

func cleanPath(p string) string {
	p = filepath.ToSlash(filepath.Clean(p))
	return path.Join("/", p)
}

func relativePath(scope, fPath string) string {
	relativePath := strings.TrimPrefix(fPath, scope)
	return strings.TrimPrefix(relativePath, "/")
}

func (s *searchOptions) matches(fPath string) bool {
	if len(s.Conditions) > 0 {
		match := false
		for _, condition := range s.Conditions {
			if condition(fPath) {
				match = true
				break
			}
		}

		if !match {
			return false
		}
	}

	if len(s.Terms) == 0 {
		return true
	}

	_, fileName := path.Split(fPath)
	if !s.CaseSensitive {
		fileName = strings.ToLower(fileName)
	}

	for _, term := range s.Terms {
		if strings.Contains(fileName, term) {
			return true
		}
	}

	return false
}

func (s *searchOptions) matchesEntry(entry indexedEntry) bool {
	if len(s.Conditions) > 0 {
		match := false
		for _, condition := range s.Conditions {
			if condition(entry.path) {
				match = true
				break
			}
		}

		if !match {
			return false
		}
	}

	if len(s.Terms) == 0 {
		return true
	}

	fileName := entry.name
	if !s.CaseSensitive {
		fileName = entry.lowerName
	}

	for _, term := range s.Terms {
		if strings.Contains(fileName, term) {
			return true
		}
	}

	return false
}

// walkUnsorted is a search-specific afero walker. Unlike afero.Walk, it avoids
// sorting directory entries and re-statting every child before matching.
func walkUnsorted(ctx context.Context, fs afero.Fs, root string, walkFn filepath.WalkFunc) error {
	info, err := lstatIfPossible(fs, root)
	if err != nil {
		return walkFn(root, nil, err)
	}

	return walkUnsortedPath(ctx, fs, root, info, walkFn)
}

func walkUnsortedPath(ctx context.Context, fs afero.Fs, fPath string, info os.FileInfo, walkFn filepath.WalkFunc) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}

	err := walkFn(fPath, info, nil)
	if err != nil {
		if info.IsDir() && err == filepath.SkipDir {
			return nil
		}
		return err
	}

	if !info.IsDir() {
		return nil
	}

	file, err := fs.Open(fPath)
	if err != nil {
		return walkDirError(fPath, info, err, walkFn)
	}

	entries, err := file.Readdir(-1)
	closeErr := file.Close()
	if err != nil {
		return walkDirError(fPath, info, err, walkFn)
	}
	if closeErr != nil {
		return walkDirError(fPath, info, closeErr, walkFn)
	}

	for _, entry := range entries {
		err = walkUnsortedPath(ctx, fs, path.Join(fPath, entry.Name()), entry, walkFn)
		if err != nil {
			if entry.IsDir() && err == filepath.SkipDir {
				continue
			}
			return err
		}
	}

	return nil
}

func walkDirError(fPath string, info os.FileInfo, err error, walkFn filepath.WalkFunc) error {
	err = walkFn(fPath, info, err)
	if err != nil && err != filepath.SkipDir {
		return err
	}

	return nil
}

func lstatIfPossible(fs afero.Fs, p string) (os.FileInfo, error) {
	if lstater, ok := fs.(afero.Lstater); ok {
		info, _, err := lstater.LstatIfPossible(p)
		return info, err
	}

	return fs.Stat(p)
}
