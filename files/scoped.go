package files

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/afero"
)

// RuleChecker reports whether a scope-relative virtual path is allowed by
// access rules. Implementations may also enforce display preferences, but
// ScopedFs prefers CheckRules when available so hide-dotfiles preferences do
// not block filesystem operations.
type RuleChecker interface {
	Check(path string) bool
}

type rulesChecker interface {
	CheckRules(path string) bool
}

// ScopedFs is an afero.Fs that confines every operation to a base directory and
// refuses to follow a symbolic link whose on-disk target resolves outside that
// base. It wraps an *afero.BasePathFs — which already provides the lexical
// confinement — and adds a per-operation scope check on every call that would
// dereference a symlink at the OS layer (open, stat, lstat, chmod, …).
type ScopedFs struct {
	base *afero.BasePathFs
	// checkerMu guards checker: SetChecker runs at request setup while guard
	// may run concurrently for the same filesystem, so the swap and the
	// reads serialize here instead of racing.
	checkerMu sync.RWMutex
	checker   RuleChecker
}

var (
	_ afero.Fs      = (*ScopedFs)(nil)
	_ afero.Lstater = (*ScopedFs)(nil)
)

// maxSymlinkHops bounds how many dangling symlinks within() will follow before
// giving up, so a pathological chain cannot loop forever. It mirrors the kernel
// MAXSYMLINKS limit; the operation is rejected once the bound is exceeded.
const maxSymlinkHops = 255

func NewScopedFs(source afero.Fs, path string) *ScopedFs {
	if s, ok := source.(*ScopedFs); ok {
		source = s.base
	}
	return &ScopedFs{base: afero.NewBasePathFs(source, path).(*afero.BasePathFs)}
}

// NewFs builds a user filesystem rooted at path. When followExternal is true it
// returns a bare BasePathFs, so symlinks whose target resolves outside the scope
// are followed; otherwise it returns a ScopedFs that refuses to follow them.
func NewFs(source afero.Fs, path string, followExternal bool) afero.Fs {
	if followExternal {
		return afero.NewBasePathFs(source, path)
	}
	return NewScopedFs(source, path)
}

// BasePath returns the underlying *afero.BasePathFs of a user filesystem built
// by NewFs, whether it is a *ScopedFs or a bare *afero.BasePathFs, or nil if it
// is neither.
func BasePath(fs afero.Fs) *afero.BasePathFs {
	switch f := fs.(type) {
	case *ScopedFs:
		return f.BasePathFs()
	case *afero.BasePathFs:
		return f
	}
	return nil
}

// BasePathFs returns the underlying *afero.BasePathFs.
func (s *ScopedFs) BasePathFs() *afero.BasePathFs { return s.base }

// RealPath resolves a scoped path to the real on-disk path by delegating to
// the underlying BasePathFs. This is needed by callers that need the actual
// filesystem path (e.g. disk.UsageWithContext).
func (s *ScopedFs) RealPath(name string) (string, error) {
	return s.base.RealPath(name)
}

// SetChecker installs an optional rule checker. When set, guarded operations
// deny paths that the checker rejects, including when a symlink resolves to a
// denied target. This makes rule enforcement structural for code that goes
// through the scoped filesystem.
func (s *ScopedFs) SetChecker(checker RuleChecker) {
	s.checkerMu.Lock()
	defer s.checkerMu.Unlock()
	s.checker = checker
}

// SetRuleChecker installs checker on fs when fs is a ScopedFs.
func SetRuleChecker(fs afero.Fs, checker RuleChecker) {
	if s, ok := fs.(*ScopedFs); ok {
		s.SetChecker(checker)
	}
}

// VirtualResolver resolves a scope-relative virtual path to its resolved
// scope-relative virtual path.
type VirtualResolver interface {
	ResolveVirtual(name string) (string, error)
}

// ResolveVirtual resolves name to its resolved scope-relative virtual path when
// fs supports it. ok reports whether the filesystem can resolve virtual paths.
func ResolveVirtual(fs afero.Fs, name string) (string, bool, error) {
	if vr, ok := fs.(VirtualResolver); ok {
		v, err := vr.ResolveVirtual(name)
		return v, true, err
	}
	if bp, ok := fs.(*afero.BasePathFs); ok {
		v, err := resolveVirtualBasePath(bp, name)
		return v, true, err
	}
	return "", false, nil
}

func resolveVirtualBasePath(base *afero.BasePathFs, name string) (string, error) {
	root, err := filepath.EvalSymlinks(afero.FullBaseFsPath(base, "/"))
	if err != nil {
		return "", err
	}

	target := afero.FullBaseFsPath(base, name)
	resolved, err := filepath.EvalSymlinks(target)
	var parts []string
	for hops := 0; errors.Is(err, fs.ErrNotExist); {
		if fi, lerr := os.Lstat(target); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			hops++
			if hops > maxSymlinkHops {
				return "", os.ErrPermission
			}
			dest, rerr := os.Readlink(target)
			if rerr != nil {
				return "", rerr
			}
			if !filepath.IsAbs(dest) {
				baseDir, berr := filepath.EvalSymlinks(filepath.Dir(target))
				if berr != nil {
					return "", berr
				}
				dest = filepath.Join(baseDir, dest)
			}
			target = filepath.Clean(dest)
			parts = nil
		} else {
			parent := filepath.Dir(target)
			if parent == target {
				break
			}
			parts = append([]string{filepath.Base(target)}, parts...)
			target = parent
		}
		resolved, err = filepath.EvalSymlinks(target)
	}
	if err != nil {
		return "", err
	}

	if resolved == root {
		if len(parts) == 0 {
			return "/", nil
		}
		return cleanVirtual("/" + strings.Join(parts, "/")), nil
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if !strings.HasPrefix(resolved, prefix) {
		// Target resolves outside the base path. Keep lexical path handling to the
		// caller; this cannot be represented as a virtual path inside base.
		return cleanVirtual(name), nil
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", err
	}
	virtual := cleanVirtual(rel)
	if len(parts) > 0 {
		virtual = cleanVirtual(virtual + "/" + strings.Join(parts, "/"))
	}
	return virtual, nil
}

// guard returns an error if name's on-disk target resolves outside the scope or
// is denied by the optional rule checker.
func (s *ScopedFs) guard(name string) error {
	s.checkerMu.RLock()
	checker := s.checker
	s.checkerMu.RUnlock()

	if checker == nil {
		ok, err := s.within(name)
		if err != nil {
			return err
		}
		if !ok {
			return os.ErrPermission
		}
		return nil
	}

	// With a checker, one resolution serves both decisions: the tail-aware
	// virtual path keeps the authorization decision identical to the one
	// ResolveVirtual reports, and an out-of-scope target fails here.
	virtual, err := s.resolveVirtualTail(name)
	if err != nil {
		return err
	}
	// Check the requested lexical path first as a cheap defense-in-depth pass,
	// then check the resolved target so a symlink alias cannot bypass a deny
	// rule. Prefer CheckRules to avoid coupling hide-dotfiles display policy
	// to filesystem operations.
	check := checker.Check
	if rc, ok := checker.(rulesChecker); ok {
		check = rc.CheckRules
	}
	if !check(cleanVirtual(name)) || !check(virtual) {
		return os.ErrPermission
	}
	return nil
}

// ResolveVirtual resolves a scope-relative virtual path to its real target with
// symlinks resolved, and returns the resulting scope-relative virtual path.
// For paths that do not exist yet, it resolves the nearest existing ancestor,
// matching within semantics, and appends the remaining tail as a virtual path.
func (s *ScopedFs) ResolveVirtual(name string) (string, error) {
	return s.resolveVirtualTail(name)
}

// resolveVirtualTail resolves name to the scope-relative virtual path of its
// real target, appending the tail below the nearest existing ancestor for
// paths that do not exist yet. Both the rule checker in guard and the public
// ResolveVirtual use it so the authorization decision never sees a collapsed
// ancestor in one place and the full path in another.
func (s *ScopedFs) resolveVirtualTail(name string) (string, error) {
	resolved, ok, tail, err := s.resolveRealTail(name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", os.ErrPermission
	}
	virtual, err := s.virtualFromResolved(resolved)
	if err != nil {
		return "", err
	}
	// Tail elements are bare names collected below the nearest existing
	// ancestor, so they join onto the scope-relative path as-is.
	if tail != "" {
		virtual = cleanVirtual(virtual + "/" + tail)
	}
	return virtual, nil
}

func (s *ScopedFs) virtualFromResolved(resolved string) (string, error) {
	root, err := filepath.EvalSymlinks(afero.FullBaseFsPath(s.base, "/"))
	if err != nil {
		return "", err
	}
	if resolved == root {
		return "/", nil
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if !strings.HasPrefix(resolved, prefix) {
		return "", os.ErrPermission
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return "", err
	}
	return cleanVirtual(rel), nil
}

func cleanVirtual(name string) string {
	name = filepath.ToSlash(name)
	if name == "" || name == "." {
		return "/"
	}
	if !strings.HasPrefix(name, "/") {
		name = "/" + name
	}
	// path.Clean would also work, but keep behavior close to existing virtual paths.
	cleaned := filepath.ToSlash(filepath.Clean(strings.ReplaceAll(name, "/", string(filepath.Separator))))
	if cleaned == "." || cleaned == "" {
		return "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned
}

func (s *ScopedFs) resolveReal(p string) (resolved string, withinScope bool, err error) {
	resolved, withinScope, _, err = s.resolveRealTail(p)
	return resolved, withinScope, err
}

// resolveRealTail behaves like resolveReal and additionally returns the tail:
// the sequence of path elements below the nearest existing ancestor, for
// paths that do not exist yet. A followed symlink resets the tail, because the
// link target replaces the path instead of extending it. The tail elements
// are bare names, so callers can join them onto a scope-relative path.
func (s *ScopedFs) resolveRealTail(p string) (resolved string, withinScope bool, tail string, err error) {
	root, err := filepath.EvalSymlinks(afero.FullBaseFsPath(s.base, "/"))
	if err != nil {
		return "", false, "", err
	}

	target := afero.FullBaseFsPath(s.base, p)
	resolved, err = filepath.EvalSymlinks(target)
	// When target does not resolve, work out where the operation would actually
	// land. A non-existent regular path resolves to the file that would be
	// created inside its containing directory, so walk up to the nearest
	// existing ancestor and validate that. But when target itself is a dangling
	// symlink, follow it one level instead: validating its lexical parent would
	// wrongly accept a link pointing outside the scope, letting a write follow
	// the link and create the file out of bounds.
	var parts []string
	for hops := 0; errors.Is(err, fs.ErrNotExist); {
		if fi, lerr := os.Lstat(target); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			hops++
			if hops > maxSymlinkHops {
				return "", false, "", os.ErrPermission
			}
			dest, rerr := os.Readlink(target)
			if rerr != nil {
				return "", false, "", rerr
			}
			if !filepath.IsAbs(dest) {
				// Resolve the link relative to the directory that really contains
				// it, not its lexical parent: a symlinked ancestor could otherwise
				// shift the computed target back into scope while the real write
				// lands outside it. The parent is guaranteed to resolve here
				// because os.Lstat above already traversed it.
				base, berr := filepath.EvalSymlinks(filepath.Dir(target))
				if berr != nil {
					return "", false, "", berr
				}
				dest = filepath.Join(base, dest)
			}
			target = filepath.Clean(dest)
			parts = nil
		} else {
			parent := filepath.Dir(target)
			if parent == target {
				break
			}
			parts = append([]string{filepath.Base(target)}, parts...)
			target = parent
		}
		resolved, err = filepath.EvalSymlinks(target)
	}
	if err != nil {
		return "", false, "", err
	}

	// Compare against root with a trailing separator so a sibling like
	// "/srvother" is not treated as being inside "/srv". When root is itself the
	// filesystem boundary (e.g. "/"), it already ends in a separator, so avoid
	// producing "//" — which no path would match — and accept any path under it.
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}

	return resolved, resolved == root || strings.HasPrefix(resolved, prefix), strings.Join(parts, "/"), nil
}

// within reports whether the on-disk target of p — after resolving any symbolic
// links — stays within the scoped root.
func (s *ScopedFs) within(p string) (bool, error) {
	_, ok, err := s.resolveReal(p)
	return ok, err
}

func (s *ScopedFs) Create(name string) (afero.File, error) {
	if err := s.guard(name); err != nil {
		return nil, err
	}
	return s.base.Create(name)
}

func (s *ScopedFs) Mkdir(name string, perm os.FileMode) error {
	if err := s.guard(name); err != nil {
		return err
	}
	return s.base.Mkdir(name, perm)
}

func (s *ScopedFs) MkdirAll(path string, perm os.FileMode) error {
	if err := s.guard(path); err != nil {
		return err
	}
	return s.base.MkdirAll(path, perm)
}

func (s *ScopedFs) Open(name string) (afero.File, error) {
	if err := s.guard(name); err != nil {
		return nil, err
	}
	return s.base.Open(name)
}

func (s *ScopedFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if err := s.guard(name); err != nil {
		return nil, err
	}
	return s.base.OpenFile(name, flag, perm)
}

func (s *ScopedFs) Remove(name string) error {
	if err := s.guard(name); err != nil {
		return err
	}
	return s.base.Remove(name)
}

func (s *ScopedFs) RemoveAll(path string) error {
	if err := s.guard(path); err != nil {
		return err
	}
	return s.base.RemoveAll(path)
}

func (s *ScopedFs) Rename(oldname, newname string) error {
	if err := s.guard(oldname); err != nil {
		return err
	}
	if err := s.guard(newname); err != nil {
		return err
	}
	return s.base.Rename(oldname, newname)
}

func (s *ScopedFs) Stat(name string) (os.FileInfo, error) {
	if err := s.guard(name); err != nil {
		return nil, err
	}
	return s.base.Stat(name)
}

func (s *ScopedFs) Name() string { return "ScopedFs" }

func (s *ScopedFs) Chmod(name string, mode os.FileMode) error {
	if err := s.guard(name); err != nil {
		return err
	}
	return s.base.Chmod(name, mode)
}

func (s *ScopedFs) Chown(name string, uid, gid int) error {
	if err := s.guard(name); err != nil {
		return err
	}
	return s.base.Chown(name, uid, gid)
}

func (s *ScopedFs) Chtimes(name string, atime, mtime time.Time) error {
	if err := s.guard(name); err != nil {
		return err
	}
	return s.base.Chtimes(name, atime, mtime)
}

func (s *ScopedFs) LstatIfPossible(name string) (os.FileInfo, bool, error) {
	if err := s.guard(name); err != nil {
		return nil, false, err
	}
	return s.base.LstatIfPossible(name)
}
