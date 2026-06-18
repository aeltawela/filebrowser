package fbhttp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asdine/storm/v3"
	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/filebrowser/filebrowser/v2/storage"
	"github.com/filebrowser/filebrowser/v2/storage/bolt"
	"github.com/filebrowser/filebrowser/v2/users"
)

func TestLinkDownloadDirect(t *testing.T) {
	st, token, scope := setupLinkDownloadTest(t, users.Permissions{Create: true, Modify: true}, true)

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="source.txt"`)
		_, _ = w.Write([]byte("downloaded"))
	}))
	t.Cleanup(source.Close)

	manager := newLinkDownloadManager()
	job := postLinkDownload(t, st, token, manager, linkDownloadRequest{
		URL:        source.URL + "/ignored",
		Path:       "/downloads",
		Downloader: linkDownloaderDirect,
	})

	final := waitForLinkDownload(t, manager, job.ID)
	if final.Status != linkDownloadStatusCompleted {
		t.Fatalf("expected completed job, got %+v", final)
	}

	got, err := os.ReadFile(filepath.Join(scope, "downloads", "source.txt"))
	if err != nil {
		t.Fatalf("expected downloaded file: %v", err)
	}
	if string(got) != "downloaded" {
		t.Fatalf("unexpected downloaded content %q", string(got))
	}
}

func TestLinkDownloadRequiresCreatePermission(t *testing.T) {
	st, token, _ := setupLinkDownloadTest(t, users.Permissions{}, true)
	manager := newLinkDownloadManager()

	body := marshalLinkDownloadRequest(t, linkDownloadRequest{
		URL:        "https://example.com/file.txt",
		Path:       "/",
		Downloader: linkDownloaderDirect,
	})
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("X-Auth", token)
	rec := httptest.NewRecorder()

	handle(linkDownloadPostHandler(manager), "", st, &settings.Server{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden, got %d body=%q", rec.Code, rec.Body.String())
	}
}

func TestLinkDownloadDirectConflictWithoutOverwrite(t *testing.T) {
	st, token, scope := setupLinkDownloadTest(t, users.Permissions{Create: true, Modify: true}, true)

	if err := os.MkdirAll(filepath.Join(scope, "downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scope, "downloads", "source.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("new"))
	}))
	t.Cleanup(source.Close)

	manager := newLinkDownloadManager()
	job := postLinkDownload(t, st, token, manager, linkDownloadRequest{
		URL:        source.URL + "/source.txt",
		Path:       "/downloads",
		Downloader: linkDownloaderDirect,
		Filename:   "source.txt",
		Overwrite:  false,
	})

	final := waitForLinkDownload(t, manager, job.ID)
	if final.Status != linkDownloadStatusFailed {
		t.Fatalf("expected failed job, got %+v", final)
	}
	if !strings.Contains(final.Error, "already exists") {
		t.Fatalf("expected conflict error, got %q", final.Error)
	}

	got, err := os.ReadFile(filepath.Join(scope, "downloads", "source.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("existing file was changed: %q", string(got))
	}
}

func TestQualityOptionsFromFormats(t *testing.T) {
	options := qualityOptionsFromFormats([]ytDLPFormat{
		{Height: 720, VCodec: "avc1", ACodec: "none"},
		{Height: 1080, VCodec: "vp9", ACodec: "none"},
		{Height: 360, VCodec: "avc1", ACodec: "mp4a"},
		{VCodec: "none", ACodec: "opus"},
	})

	want := []linkDownloadQualityData{
		{Label: "Best available", Quality: "bestvideo*+bestaudio/best"},
		{Label: "1080p", Quality: "bv*[height<=1080]+ba/b[height<=1080]/wv*+ba/w"},
		{Label: "720p", Quality: "bv*[height<=720]+ba/b[height<=720]/wv*+ba/w"},
		{Label: "360p", Quality: "bv*[height<=360]+ba/b[height<=360]/wv*+ba/w"},
		{Label: "Audio only", Quality: "bestaudio/best"},
	}

	if len(options) != len(want) {
		t.Fatalf("expected %d options, got %d: %+v", len(want), len(options), options)
	}

	for i := range want {
		if options[i] != want[i] {
			t.Fatalf("option %d: expected %+v, got %+v", i, want[i], options[i])
		}
	}
}

func setupLinkDownloadTest(t *testing.T, perm users.Permissions, enabled bool) (*storage.Storage, string, string) {
	t.Helper()

	scope := t.TempDir()
	key := []byte("test-signing-key")

	db, err := storm.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	st, err := bolt.NewStorage(db)
	if err != nil {
		t.Fatalf("failed to get storage: %v", err)
	}
	if err := st.Users.Save(&users.User{Username: "u", Password: "pw", Perm: perm}); err != nil {
		t.Fatalf("failed to save user: %v", err)
	}
	set := &settings.Settings{
		Key: key,
		LinkDownload: settings.LinkDownload{
			Enabled:        enabled,
			DefaultQuality: settings.DefaultLinkDownloadQuality,
			Downloader:     linkDownloaderDirect,
			YTDLPPath:      settings.DefaultLinkDownloadYTDLPPath,
		},
	}
	if err := st.Settings.Save(set); err != nil {
		t.Fatalf("failed to save settings: %v", err)
	}

	st.Users = &customFSUser{
		Store: st.Users,
		fs:    afero.NewBasePathFs(afero.NewOsFs(), scope),
	}

	return st, signToken(t, perm, key), scope
}

func postLinkDownload(t *testing.T, st *storage.Storage, token string, manager *linkDownloadManager, reqBody linkDownloadRequest) linkDownloadJobData {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/", marshalLinkDownloadRequest(t, reqBody))
	req.Header.Set("X-Auth", token)
	rec := httptest.NewRecorder()

	handle(linkDownloadPostHandler(manager), "", st, &settings.Server{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected accepted, got %d body=%q", rec.Code, rec.Body.String())
	}

	var job linkDownloadJobData
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
		t.Fatalf("failed to parse job response: %v", err)
	}
	return job
}

func marshalLinkDownloadRequest(t *testing.T, req linkDownloadRequest) *bytes.Reader {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(body)
}

func waitForLinkDownload(t *testing.T, manager *linkDownloadManager, id string) linkDownloadJobData {
	t.Helper()

	var snapshot linkDownloadJobData
	for i := 0; i < 100; i++ {
		job, ok := manager.get(id, 1, false)
		if !ok {
			t.Fatalf("job %q not found", id)
		}
		snapshot = job.snapshot()
		switch snapshot.Status {
		case linkDownloadStatusCompleted, linkDownloadStatusFailed, linkDownloadStatusCanceled:
			return snapshot
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("job %q did not finish, last snapshot %+v", id, snapshot)
	return snapshot
}
