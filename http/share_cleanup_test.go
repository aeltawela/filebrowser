package fbhttp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/filebrowser/filebrowser/v2/diskcache"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/share"
	"github.com/filebrowser/filebrowser/v2/users"
)

func saveTestShare(t *testing.T, st *share.Storage, hash, path string, userID uint) {
	t.Helper()

	if err := st.Save(&share.Link{Hash: hash, Path: path, UserID: userID}); err != nil {
		t.Fatalf("failed to save share %s: %v", hash, err)
	}
}

func shareExists(t *testing.T, st *share.Storage, hash string) bool {
	t.Helper()

	_, err := st.GetByHash(hash)
	return err == nil
}

// Regression for GHSA-r6pg-pg54-rcr5: deleting a file must remove every link
// that the deletion invalidated, not only the deleter's links. Otherwise the
// surviving link starts serving whatever file later appears at that path.
func TestDeletePurgesOtherUsersShares(t *testing.T) {
	userScope := t.TempDir()
	if err := os.WriteFile(filepath.Join(userScope, "shared.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Delete: true, Download: true, Share: true}
	st := scopedUserStorage(t, userScope, perm, key)

	saveTestShare(t, st.Share, "owner", "/shared.txt", 1)
	saveTestShare(t, st.Share, "foreign", "/shared.txt", 2)
	saveTestShare(t, st.Share, "unrelated", "/other.txt", 1)

	req, _ := http.NewRequest(http.MethodDelete, "/shared.txt", http.NoBody)
	req.Header.Set("X-Auth", signToken(t, perm, key))
	rec := httptest.NewRecorder()
	handle(resourceDeleteHandler(diskcache.NewNoOp()), "", st, &settings.Server{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%q", rec.Code, rec.Body.String())
	}
	if shareExists(t, st.Share, "owner") {
		t.Error("deleter's share survived the delete")
	}
	if shareExists(t, st.Share, "foreign") {
		t.Error("VULNERABLE: another user's share survived the delete and can serve a future file")
	}
	if !shareExists(t, st.Share, "unrelated") {
		t.Error("unrelated share was removed")
	}
}

// Regression for GHSA-m8v4-4w34-rrvf: renaming a shared file must remove the
// link at the old path instead of leaving it to serve a future file.
func TestRenamePurgesShares(t *testing.T) {
	userScope := t.TempDir()
	if err := os.WriteFile(filepath.Join(userScope, "a.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userScope, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true, Modify: true, Rename: true, Download: true, Share: true}
	st := scopedUserStorage(t, userScope, perm, key)

	saveTestShare(t, st.Share, "old", "/a.txt", 1)
	saveTestShare(t, st.Share, "foreign", "/a.txt", 2)
	saveTestShare(t, st.Share, "keep", "/keep.txt", 1)

	req, _ := http.NewRequest(http.MethodPatch, "/a.txt?action=rename&destination=/b.txt", http.NoBody)
	req.Header.Set("X-Auth", signToken(t, perm, key))
	rec := httptest.NewRecorder()
	handle(resourcePatchHandler(diskcache.NewNoOp()), "", st, &settings.Server{}).ServeHTTP(rec, req)

	if rec.Code >= 400 {
		t.Fatalf("rename failed with %d body=%q", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(userScope, "b.txt")); err != nil {
		t.Fatalf("rename did not move the file: %v", err)
	}
	if shareExists(t, st.Share, "old") {
		t.Error("VULNERABLE: share at the old path survived the rename")
	}
	if shareExists(t, st.Share, "foreign") {
		t.Error("VULNERABLE: another user's share at the old path survived the rename")
	}
	if !shareExists(t, st.Share, "keep") {
		t.Error("unrelated share was removed")
	}

	// Re-creating a file at the old path must not resurrect the link.
	if err := os.WriteFile(filepath.Join(userScope, "a.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if shareExists(t, st.Share, "old") {
		t.Error("share was still present after a new file appeared at the old path")
	}
}

// A directory rename must also purge links to descendants, not just the
// directory itself.
func TestRenamePurgesDescendantShares(t *testing.T) {
	userScope := t.TempDir()
	if err := os.MkdirAll(filepath.Join(userScope, "dir", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userScope, "dir", "sub", "file.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	key := []byte("test-signing-key")
	perm := users.Permissions{Create: true, Modify: true, Rename: true, Download: true, Share: true}
	st := scopedUserStorage(t, userScope, perm, key)

	saveTestShare(t, st.Share, "dir", "/dir", 1)
	saveTestShare(t, st.Share, "child", "/dir/sub/file.txt", 1)

	req, _ := http.NewRequest(http.MethodPatch, "/dir?action=rename&destination=/moved", http.NoBody)
	req.Header.Set("X-Auth", signToken(t, perm, key))
	rec := httptest.NewRecorder()
	handle(resourcePatchHandler(diskcache.NewNoOp()), "", st, &settings.Server{}).ServeHTTP(rec, req)

	if rec.Code >= 400 {
		t.Fatalf("rename failed with %d body=%q", rec.Code, rec.Body.String())
	}
	if shareExists(t, st.Share, "dir") {
		t.Error("directory share survived the rename")
	}
	if shareExists(t, st.Share, "child") {
		t.Error("descendant share survived the rename")
	}
}
