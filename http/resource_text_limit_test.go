package fbhttp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/asdine/storm/v3"
	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/files"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/storage/bolt"
	"github.com/filebrowser/filebrowser/v2/users"
)

// oversizedReader serves more bytes than the file's stat size claims, standing
// in for a file that grows between the size check and the read.
type oversizedReader struct {
	afero.File
	remaining int
}

func (f *oversizedReader) Read(p []byte) (int, error) {
	if f.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > f.remaining {
		n = f.remaining
	}
	for i := range p[:n] {
		p[i] = 'A'
	}
	f.remaining -= n
	return n, nil
}

// alwaysGrowingFs serves oversized content on every open while stat claims a
// small file, reproducing a grow race inside detectType's content read.
type alwaysGrowingFs struct {
	afero.Fs
}

func (g *alwaysGrowingFs) Open(name string) (afero.File, error) {
	f, err := g.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	if filepath.Base(name) == "big.txt" {
		return &oversizedReader{File: f, remaining: files.MaxTextContentBytes + 1}, nil
	}
	return f, nil
}

// growingFs reports a small stat size for big.txt, serves its real (small)
// content on the first open, and oversized content on every later open. This
// reproduces the stat-to-read race from GHSA-7xqm-7738-642x deterministically.
type growingFs struct {
	afero.Fs
	opens int
}

func (g *growingFs) Open(name string) (afero.File, error) {
	f, err := g.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	if filepath.Base(name) == "big.txt" {
		g.opens++
		if g.opens > 1 {
			return &oversizedReader{File: f, remaining: files.MaxTextContentBytes + 1}, nil
		}
	}
	return f, nil
}

// Regression for GHSA-7xqm-7738-642x: the text gate uses the stat size, but a
// file that grows before the content read must not be buffered in full. The
// handler must refuse the oversized read instead of returning it.
func TestTextReadBoundedWhenFileGrowsAfterStat(t *testing.T) {
	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "/big.txt", []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	lfs := &growingFs{Fs: mem}

	db, err := storm.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	st, err := bolt.NewStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	perm := users.Permissions{Download: true, Modify: true}
	if err := st.Users.Save(&users.User{Username: "u", Password: "pw", Perm: perm}); err != nil {
		t.Fatal(err)
	}
	key := []byte("test-signing-key")
	if err := st.Settings.Save(&settings.Settings{Key: key}); err != nil {
		t.Fatal(err)
	}
	st.Users = &customFSUser{Store: st.Users, fs: lfs, followExternal: true}

	req, _ := http.NewRequest(http.MethodGet, "/big.txt", http.NoBody)
	req.Header.Set("X-Auth", signToken(t, perm, key))
	req.Header.Set("X-Encoding", "true")
	rec := httptest.NewRecorder()
	handle(resourceGetHandler, "", st, &settings.Server{}).ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("VULNERABLE: oversized content was served (%d bytes)", rec.Body.Len())
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for the oversized read, got %d body=%q", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() > files.MaxTextContentBytes {
		t.Fatalf("response buffered more than the cap: %d bytes", rec.Body.Len())
	}
}

// A stat-to-read grow race inside detectType must fail closed with 413, the
// same status as the explicit size gate, not a 500.
func TestDetectTypeGrowRaceReturns413(t *testing.T) {
	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "/big.txt", []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	lfs := &alwaysGrowingFs{Fs: mem}

	db, err := storm.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	st, err := bolt.NewStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	perm := users.Permissions{Download: true, Modify: true}
	if err := st.Users.Save(&users.User{Username: "u", Password: "pw", Perm: perm}); err != nil {
		t.Fatal(err)
	}
	key := []byte("test-signing-key")
	if err := st.Settings.Save(&settings.Settings{Key: key}); err != nil {
		t.Fatal(err)
	}
	st.Users = &customFSUser{Store: st.Users, fs: lfs, followExternal: true}

	req, _ := http.NewRequest(http.MethodGet, "/big.txt", http.NoBody)
	req.Header.Set("X-Auth", signToken(t, perm, key))
	req.Header.Set("X-Encoding", "true")
	rec := httptest.NewRecorder()
	handle(resourceGetHandler, "", st, &settings.Server{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for the oversized content read, got %d body=%q", rec.Code, rec.Body.String())
	}
}
