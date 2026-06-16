package search

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/filebrowser/filebrowser/v2/files"
	"github.com/filebrowser/filebrowser/v2/rules"
	"github.com/spf13/afero"
)

type checkerFunc func(string) bool

func (f checkerFunc) Check(p string) bool {
	return f(p)
}

func TestSearchMatchesTermsAndTypes(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	mustWriteFile(t, fs, "/media/photos/family.jpg")
	mustWriteFile(t, fs, "/media/photos/family.txt")
	mustWriteFile(t, fs, "/media/videos/family.mp4")

	got := collectSearch(t, fs, "/media", "type:image family", allowAll)
	want := []string{"photos/family.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Search() = %v, want %v", got, want)
	}
}

func TestSearchCaseSensitivity(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	mustWriteFile(t, fs, "/docs/Readme.md")

	got := collectSearch(t, fs, "/docs", "read", allowAll)
	want := []string{"Readme.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Search() case-insensitive = %v, want %v", got, want)
	}

	got = collectSearch(t, fs, "/docs", "case:sensitive read", allowAll)
	if len(got) != 0 {
		t.Fatalf("Search() case-sensitive = %v, want no results", got)
	}
}

func TestSearchPrunesDeniedDirectories(t *testing.T) {
	t.Parallel()

	base := afero.NewMemMapFs()
	mustWriteFile(t, base, "/allowed/visible.txt")
	mustWriteFile(t, base, "/blocked/secret.txt")
	fs := &trackingFs{Fs: base}

	checker := checkerFunc(func(p string) bool {
		return p != "/blocked"
	})

	got := collectSearch(t, fs, "/", "secret", checker)
	if len(got) != 0 {
		t.Fatalf("Search() = %v, want no results from denied directory", got)
	}

	for _, opened := range fs.opened {
		if opened == "/blocked" {
			t.Fatalf("Search() opened denied directory %q", opened)
		}
	}
}

func TestSearchSkipsUnreadableDirectories(t *testing.T) {
	t.Parallel()

	base := afero.NewMemMapFs()
	mustWriteFile(t, base, "/broken/secret.txt")
	mustWriteFile(t, base, "/ok/match.txt")
	fs := &trackingFs{
		Fs: base,
		openErrors: map[string]error{
			"/broken": errors.New("permission denied"),
		},
	}

	got := collectSearch(t, fs, "/", "match", allowAll)
	want := []string{"ok/match.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Search() = %v, want %v", got, want)
	}
}

func TestSearchUsesCachedIndexUntilInvalidated(t *testing.T) {
	t.Parallel()

	base := afero.NewMemMapFs()
	mustWriteFile(t, base, "/docs/visible.txt")
	fs := &trackingFs{Fs: base}
	checker := keyedChecker{key: "allow-all", check: func(string) bool { return true }}

	got := collectSearch(t, fs, "/", "visible", checker)
	want := []string{"docs/visible.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Search() = %v, want %v", got, want)
	}
	openedAfterBuild := len(fs.opened)

	got = collectSearch(t, fs, "/", "visible", checker)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cached Search() = %v, want %v", got, want)
	}
	if len(fs.opened) != openedAfterBuild {
		t.Fatalf("cached Search() opened directories again: before=%d after=%d", openedAfterBuild, len(fs.opened))
	}

	mustWriteFile(t, base, "/docs/new-visible.txt")
	Invalidate(fs)

	got = collectSearch(t, fs, "/", "new-visible", checker)
	want = []string{"docs/new-visible.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Search() after Invalidate() = %v, want %v", got, want)
	}
	if len(fs.opened) == openedAfterBuild {
		t.Fatalf("Search() after Invalidate() did not rebuild the index")
	}
}

func TestSearchLoadsPersistentIndex(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	indexDir := t.TempDir()
	checker := keyedChecker{key: "allow-all", check: func(string) bool { return true }}

	fs := &trackingFs{Fs: files.NewScopedFs(afero.NewOsFs(), root)}
	mustWriteFile(t, fs, "/docs/persisted.txt")

	cache := newIndexCache()
	if err := cache.setPersistentDir(indexDir); err != nil {
		t.Fatalf("setPersistentDir() error = %v", err)
	}
	index, ok, err := cache.get(context.Background(), fs, "/", checker)
	if err != nil || !ok {
		t.Fatalf("cache.get() = (%v, %v), want indexed result", ok, err)
	}
	got := collectIndexedSearch(t, index, "persisted", checker)
	want := []string{"docs/persisted.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("indexed search = %v, want %v", got, want)
	}

	reloadedFs := &trackingFs{Fs: files.NewScopedFs(afero.NewOsFs(), root)}
	reloaded := newIndexCache()
	if err := reloaded.setPersistentDir(indexDir); err != nil {
		t.Fatalf("setPersistentDir() error = %v", err)
	}
	index, ok, err = reloaded.get(context.Background(), reloadedFs, "/", checker)
	if err != nil || !ok {
		t.Fatalf("reloaded cache.get() = (%v, %v), want persisted index", ok, err)
	}
	if len(reloadedFs.opened) != 0 {
		t.Fatalf("persistent index load opened filesystem paths: %v", reloadedFs.opened)
	}

	got = collectIndexedSearch(t, index, "persisted", checker)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted indexed search = %v, want %v", got, want)
	}

	time.Sleep(10 * time.Millisecond)
	external := filepath.Join(root, "docs", "external.txt")
	if err := os.WriteFile(external, []byte("external"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", external, err)
	}

	changedFs := &trackingFs{Fs: files.NewScopedFs(afero.NewOsFs(), root)}
	changed := newIndexCache()
	if err := changed.setPersistentDir(indexDir); err != nil {
		t.Fatalf("setPersistentDir() error = %v", err)
	}
	index, ok, err = changed.get(context.Background(), changedFs, "/", checker)
	if err != nil || !ok {
		t.Fatalf("changed cache.get() = (%v, %v), want rebuilt index", ok, err)
	}
	if len(changedFs.opened) == 0 {
		t.Fatalf("changed cache.get() reused stale persistent index without rebuilding")
	}

	got = collectIndexedSearch(t, index, "external", checker)
	want = []string{"docs/external.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rebuilt indexed search = %v, want %v", got, want)
	}
}

func BenchmarkSearchLargeTree(b *testing.B) {
	fs := afero.NewMemMapFs()
	checker := keyedChecker{key: "benchmark", check: func(string) bool { return true }}
	for dir := 0; dir < 100; dir++ {
		for file := 0; file < 50; file++ {
			name := fmt.Sprintf("/dir-%03d/file-%03d.txt", dir, file)
			if dir == 73 && file == 31 {
				name = fmt.Sprintf("/dir-%03d/needle-%03d.txt", dir, file)
			}
			mustWriteBenchmarkFile(b, fs, name)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count := 0
		err := Search(context.Background(), fs, "/", "needle", checker, func(string, os.FileInfo) error {
			count++
			return nil
		})
		if err != nil {
			b.Fatalf("Search() error = %v", err)
		}
		if count != 1 {
			b.Fatalf("Search() found %d results, want 1", count)
		}
	}
}

func collectSearch(t *testing.T, fs afero.Fs, scope, query string, checker rules.Checker) []string {
	t.Helper()

	var got []string
	err := Search(context.Background(), fs, scope, query, checker, func(p string, _ os.FileInfo) error {
		got = append(got, p)
		return nil
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	sort.Strings(got)
	return got
}

func collectIndexedSearch(t *testing.T, index *searchIndex, query string, checker rules.Checker) []string {
	t.Helper()

	var got []string
	opts := parseSearch(query)
	err := index.search(context.Background(), opts, checker, func(p string, _ os.FileInfo) error {
		got = append(got, p)
		return nil
	})
	if err != nil {
		t.Fatalf("index.search() error = %v", err)
	}

	sort.Strings(got)
	return got
}

func mustWriteFile(t *testing.T, fs afero.Fs, name string) {
	t.Helper()

	if err := fs.MkdirAll(path.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path.Dir(name), err)
	}
	if err := afero.WriteFile(fs, name, []byte(name), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", name, err)
	}
}

func mustWriteBenchmarkFile(b *testing.B, fs afero.Fs, name string) {
	b.Helper()

	if err := fs.MkdirAll(path.Dir(name), 0o755); err != nil {
		b.Fatalf("MkdirAll(%q) error = %v", path.Dir(name), err)
	}
	if err := afero.WriteFile(fs, name, []byte(name), 0o644); err != nil {
		b.Fatalf("WriteFile(%q) error = %v", name, err)
	}
}

var allowAll = checkerFunc(func(string) bool { return true })

type keyedChecker struct {
	key   string
	check func(string) bool
}

func (c keyedChecker) Check(p string) bool {
	return c.check(p)
}

func (c keyedChecker) SearchCacheKey() string {
	return c.key
}

type trackingFs struct {
	afero.Fs
	opened     []string
	openErrors map[string]error
}

func (fs *trackingFs) Open(name string) (afero.File, error) {
	name = cleanPath(name)
	fs.opened = append(fs.opened, name)
	if err, ok := fs.openErrors[name]; ok {
		return nil, err
	}

	return fs.Fs.Open(name)
}

func (fs *trackingFs) RealPath(name string) (string, error) {
	if realPathFs, ok := fs.Fs.(realPathFs); ok {
		return realPathFs.RealPath(name)
	}

	return "", os.ErrInvalid
}
