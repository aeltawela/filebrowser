package search

import (
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/rules"
)

const (
	indexMaxAge             = 30 * 24 * time.Hour
	indexValidationInterval = time.Minute
	indexVersion            = 2
)

var defaultIndexes = newIndexCache()

type realPathFs interface {
	RealPath(string) (string, error)
}

type cacheKeyChecker interface {
	SearchCacheKey() string
}

type indexKey struct {
	fsID         string
	visibilityID string
	scope        string
}

type indexCache struct {
	mu         sync.Mutex
	indexes    map[indexKey]*indexEntry
	persistent string
}

type indexEntry struct {
	ready   chan struct{}
	index   *searchIndex
	created time.Time
	checked time.Time
}

type searchIndex struct {
	scope       string
	created     time.Time
	directories []indexedDirectory
	entries     []indexedEntry
	all         []uint32
	trigrams    map[trigram][]uint32
}

type indexedDirectory struct {
	path    string
	size    int64
	modTime time.Time
}

type indexedEntry struct {
	path         string
	relativePath string
	name         string
	lowerName    string
	info         os.FileInfo
}

type trigram [3]byte

type persistedIndex struct {
	Version  int
	Scope    string
	Created  time.Time
	Dirs     []persistedDirectory
	Entries  []persistedEntry
	Trigrams map[trigram][]uint32
}

type persistedDirectory struct {
	Path    string
	Size    int64
	ModTime time.Time
}

type persistedEntry struct {
	Path         string
	RelativePath string
	Name         string
	LowerName    string
	Size         int64
	Mode         os.FileMode
	ModTime      time.Time
	IsDir        bool
}

type persistedFileInfo struct {
	name    string
	size    int64
	mode    os.FileMode
	modTime time.Time
	isDir   bool
}

func newIndexCache() *indexCache {
	return &indexCache{
		indexes: map[indexKey]*indexEntry{},
	}
}

// SetPersistentDir configures where search indexes are persisted. Passing an
// empty directory disables persistence while keeping in-memory indexing.
func SetPersistentDir(dir string) error {
	return defaultIndexes.setPersistentDir(dir)
}

func (c *indexCache) setPersistentDir(dir string) error {
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}

	c.mu.Lock()
	c.persistent = dir
	c.mu.Unlock()
	return nil
}

// Invalidate clears cached search indexes for fs. Filebrowser calls this after
// filesystem mutations so future searches rebuild against the current tree.
func Invalidate(fs afero.Fs) {
	key, ok := cacheFSID(fs)
	if !ok {
		return
	}

	defaultIndexes.invalidate(key)
}

func (c *indexCache) invalidate(fsID string) {
	dir := ""
	c.mu.Lock()
	for key := range c.indexes {
		if key.fsID == fsID {
			delete(c.indexes, key)
		}
	}
	if c.persistent != "" {
		dir = filepath.Join(c.persistent, hashString(fsID))
	}
	c.mu.Unlock()

	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

func (c *indexCache) get(ctx context.Context, fs afero.Fs, scope string, checker rules.Checker) (*searchIndex, bool, error) {
	fsID, ok := cacheFSID(fs)
	if !ok {
		return nil, false, nil
	}
	visibilityID, ok := checkerCacheID(checker)
	if !ok {
		return nil, false, nil
	}

	key := indexKey{fsID: fsID, visibilityID: visibilityID, scope: scope}
	for {
		now := time.Now()

		c.mu.Lock()
		entry := c.indexes[key]
		if entry != nil && entry.index != nil && now.Sub(entry.created) < indexMaxAge {
			index := entry.index
			if now.Sub(entry.checked) >= indexValidationInterval {
				persistentPath := c.persistentPath(key)
				c.mu.Unlock()

				valid, err := index.valid(ctx, fs)
				if err != nil {
					return nil, true, err
				}
				if !valid {
					if persistentPath != "" {
						_ = os.Remove(persistentPath)
					}
					c.mu.Lock()
					if c.indexes[key] == entry {
						delete(c.indexes, key)
					}
					c.mu.Unlock()
					continue
				}

				c.mu.Lock()
				if c.indexes[key] == entry {
					entry.checked = time.Now()
					index = entry.index
				}
			}
			c.mu.Unlock()
			return index, true, nil
		}

		if entry == nil || entry.index != nil {
			entry = &indexEntry{ready: make(chan struct{})}
			c.indexes[key] = entry
			persistentPath := c.persistentPath(key)
			c.mu.Unlock()

			index, loaded, err := loadPersistentIndex(persistentPath, scope)
			if err != nil {
				index = nil
				loaded = false
			}
			if loaded {
				valid, validateErr := index.valid(ctx, fs)
				if validateErr != nil {
					err = validateErr
				} else if !valid {
					loaded = false
					index = nil
					_ = os.Remove(persistentPath)
				}
			}
			if !loaded {
				index, err = buildSearchIndex(ctx, fs, scope, checker)
				if err == nil {
					_ = savePersistentIndex(persistentPath, index)
				}
			}

			c.mu.Lock()
			if c.indexes[key] == entry {
				if err != nil {
					delete(c.indexes, key)
				} else {
					entry.index = index
					entry.created = index.created
					entry.checked = time.Now()
				}
			}
			close(entry.ready)
			c.mu.Unlock()

			return index, true, err
		}

		ready := entry.ready
		c.mu.Unlock()

		select {
		case <-ready:
		case <-ctx.Done():
			return nil, true, context.Cause(ctx)
		}
	}
}

func (c *indexCache) persistentPath(key indexKey) string {
	if c.persistent == "" {
		return ""
	}

	return filepath.Join(
		c.persistent,
		hashString(key.fsID),
		hashString(key.visibilityID+"\x00"+key.scope)+".gob",
	)
}

func checkerCacheID(checker rules.Checker) (string, bool) {
	if checker == nil {
		return "", false
	}
	if keyer, ok := checker.(cacheKeyChecker); ok {
		key := keyer.SearchCacheKey()
		return key, key != ""
	}

	return "", false
}

func cacheFSID(fs afero.Fs) (string, bool) {
	if fs == nil {
		return "", false
	}
	if realPathFs, ok := fs.(realPathFs); ok {
		realPath, err := realPathFs.RealPath("/")
		if err == nil {
			return "real:" + realPath, true
		}
	}

	value := reflect.ValueOf(fs)
	if value.Kind() != reflect.Pointer && value.Kind() != reflect.UnsafePointer {
		return "", false
	}

	return value.Type().String() + ":" + strconv.FormatUint(uint64(value.Pointer()), 16), true
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func buildSearchIndex(ctx context.Context, fs afero.Fs, scope string, checker rules.Checker) (*searchIndex, error) {
	index := &searchIndex{
		scope:       scope,
		created:     time.Now(),
		directories: []indexedDirectory{},
		entries:     []indexedEntry{},
		all:         []uint32{},
		trigrams:    map[trigram][]uint32{},
	}

	err := walkUnsorted(ctx, fs, scope, func(fPath string, info os.FileInfo, err error) error {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if err != nil {
			return nil
		}
		if fPath == scope {
			if info.IsDir() {
				index.addDirectory(fPath, info)
			}
			return nil
		}
		if !checker.Check(fPath) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			index.addDirectory(fPath, info)
		}

		id := uint32(len(index.entries))
		name := info.Name()
		lowerName := strings.ToLower(name)
		index.entries = append(index.entries, indexedEntry{
			path:         fPath,
			relativePath: relativePath(scope, fPath),
			name:         name,
			lowerName:    lowerName,
			info:         info,
		})
		index.all = append(index.all, id)
		index.addTrigrams(id, lowerName)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return index, nil
}

func (idx *searchIndex) addDirectory(dirPath string, info os.FileInfo) {
	idx.directories = append(idx.directories, indexedDirectory{
		path:    dirPath,
		size:    info.Size(),
		modTime: info.ModTime(),
	})
}

func (idx *searchIndex) valid(ctx context.Context, fs afero.Fs) (bool, error) {
	if len(idx.directories) == 0 || time.Since(idx.created) > indexMaxAge {
		return false, nil
	}

	for _, dir := range idx.directories {
		if ctx.Err() != nil {
			return false, context.Cause(ctx)
		}

		info, err := lstatIfPossible(fs, dir.path)
		if err != nil || !info.IsDir() {
			return false, nil
		}
		if info.Size() != dir.size || !info.ModTime().Equal(dir.modTime) {
			return false, nil
		}
	}

	return true, nil
}

func loadPersistentIndex(name, scope string) (*searchIndex, bool, error) {
	if name == "" {
		return nil, false, nil
	}

	file, err := os.Open(name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer file.Close()

	var persisted persistedIndex
	if err := gob.NewDecoder(file).Decode(&persisted); err != nil {
		_ = os.Remove(name)
		return nil, false, nil
	}
	if persisted.Version != indexVersion || persisted.Scope != scope || time.Since(persisted.Created) > indexMaxAge {
		_ = os.Remove(name)
		return nil, false, nil
	}

	index := &searchIndex{
		scope:       persisted.Scope,
		created:     persisted.Created,
		directories: make([]indexedDirectory, 0, len(persisted.Dirs)),
		entries:     make([]indexedEntry, 0, len(persisted.Entries)),
		all:         make([]uint32, 0, len(persisted.Entries)),
		trigrams:    persisted.Trigrams,
	}
	for _, dir := range persisted.Dirs {
		index.directories = append(index.directories, indexedDirectory{
			path:    dir.Path,
			size:    dir.Size,
			modTime: dir.ModTime,
		})
	}
	for i, entry := range persisted.Entries {
		index.entries = append(index.entries, indexedEntry{
			path:         entry.Path,
			relativePath: entry.RelativePath,
			name:         entry.Name,
			lowerName:    entry.LowerName,
			info: persistedFileInfo{
				name:    entry.Name,
				size:    entry.Size,
				mode:    entry.Mode,
				modTime: entry.ModTime,
				isDir:   entry.IsDir,
			},
		})
		index.all = append(index.all, uint32(i))
	}
	if index.trigrams == nil {
		index.trigrams = map[trigram][]uint32{}
		for id, entry := range index.entries {
			index.addTrigrams(uint32(id), entry.lowerName)
		}
	}

	return index, true, nil
}

func savePersistentIndex(name string, index *searchIndex) error {
	if name == "" || index == nil {
		return nil
	}

	dir := filepath.Dir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".search-index-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	persisted := persistedIndex{
		Version:  indexVersion,
		Scope:    index.scope,
		Created:  index.created,
		Dirs:     make([]persistedDirectory, 0, len(index.directories)),
		Entries:  make([]persistedEntry, 0, len(index.entries)),
		Trigrams: index.trigrams,
	}
	if persisted.Created.IsZero() {
		persisted.Created = time.Now()
	}
	for _, dir := range index.directories {
		persisted.Dirs = append(persisted.Dirs, persistedDirectory{
			Path:    dir.path,
			Size:    dir.size,
			ModTime: dir.modTime,
		})
	}
	for _, entry := range index.entries {
		persisted.Entries = append(persisted.Entries, persistedEntry{
			Path:         entry.path,
			RelativePath: entry.relativePath,
			Name:         entry.name,
			LowerName:    entry.lowerName,
			Size:         entry.info.Size(),
			Mode:         entry.info.Mode(),
			ModTime:      entry.info.ModTime(),
			IsDir:        entry.info.IsDir(),
		})
	}

	encodeErr := gob.NewEncoder(tmp).Encode(&persisted)
	closeErr := tmp.Close()
	if encodeErr != nil {
		return encodeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}

	return os.Rename(tmpName, name)
}

func (idx *searchIndex) addTrigrams(id uint32, value string) {
	if len(value) < 3 {
		return
	}

	seen := map[trigram]struct{}{}
	for i := 0; i <= len(value)-3; i++ {
		gram := trigram{value[i], value[i+1], value[i+2]}
		if _, ok := seen[gram]; ok {
			continue
		}

		seen[gram] = struct{}{}
		idx.trigrams[gram] = append(idx.trigrams[gram], id)
	}
}

func (idx *searchIndex) search(ctx context.Context, opts *searchOptions, checker rules.Checker, found func(path string, f os.FileInfo) error) error {
	allowed := map[string]bool{}
	for _, id := range idx.candidates(opts) {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}

		entry := idx.entries[id]
		if !allowedByChecker(checker, entry.path, allowed) {
			continue
		}
		if !opts.matchesEntry(entry) {
			continue
		}
		if err := found(entry.relativePath, entry.info); err != nil {
			return err
		}
	}

	return nil
}

func (idx *searchIndex) candidates(opts *searchOptions) []uint32 {
	if len(opts.Terms) == 0 {
		return idx.all
	}

	var candidates []uint32
	seen := map[uint32]struct{}{}
	for _, term := range opts.Terms {
		if opts.CaseSensitive {
			term = strings.ToLower(term)
		}
		if len(term) < 3 {
			return idx.all
		}

		ids := idx.termCandidates(term)
		for _, id := range ids {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			candidates = append(candidates, id)
		}
	}

	return candidates
}

func (idx *searchIndex) termCandidates(term string) []uint32 {
	var best []uint32
	for i := 0; i <= len(term)-3; i++ {
		gram := trigram{term[i], term[i+1], term[i+2]}
		ids, ok := idx.trigrams[gram]
		if !ok {
			return nil
		}
		if best == nil || len(ids) < len(best) {
			best = ids
		}
	}

	return best
}

func allowedByChecker(checker rules.Checker, p string, cache map[string]bool) bool {
	if p == "/" {
		return true
	}
	if allowed, ok := cache[p]; ok {
		return allowed
	}

	parent := path.Dir(p)
	if parent != "/" && !allowedByChecker(checker, parent, cache) {
		cache[p] = false
		return false
	}

	allowed := checker.Check(p)
	cache[p] = allowed
	return allowed
}

func (i persistedFileInfo) Name() string       { return i.name }
func (i persistedFileInfo) Size() int64        { return i.size }
func (i persistedFileInfo) Mode() os.FileMode  { return i.mode }
func (i persistedFileInfo) ModTime() time.Time { return i.modTime }
func (i persistedFileInfo) IsDir() bool        { return i.isDir }
func (i persistedFileInfo) Sys() interface{}   { return nil }
