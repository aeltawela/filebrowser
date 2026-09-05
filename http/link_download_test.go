package fbhttp

import (
	"bytes"
	"context"
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
		{FormatID: "18", Width: 640, Height: 360, VCodec: "avc1", ACodec: "mp4a", Ext: "mp4"},
		{FormatID: "140", VCodec: "none", ACodec: "mp4a", Ext: "m4a", Filesize: 1000},
		{FormatID: "401", Width: 3840, Height: 2160, FPS: 60, DynamicRange: "HDR10", VCodec: "av01", ACodec: "none", Ext: "mp4", Filesize: 9000},
	})
	if options[0].Quality != settings.DefaultLinkDownloadQuality {
		t.Fatal("4K must remain the default")
	}
	var found bool
	for _, option := range options {
		if option.Quality == "401+140" {
			found = true
			for _, detail := range []string{"4K", "60 fps", "HDR10", "MKV"} {
				if !strings.Contains(option.Label, detail) {
					t.Errorf("missing %q in %q", detail, option.Label)
				}
			}
			if !strings.Contains(option.Description, "audio") || !strings.Contains(option.Description, "9.8 KiB") {
				t.Errorf("missing merged size/audio explanation: %s", option.Description)
			}
		}
	}
	if !found {
		t.Fatalf("missing exact 4K video and audio pair: %+v", options)
	}
}

func TestQualityOptionsExcludeDRMAndSilentVideo(t *testing.T) {
	options := qualityOptionsFromFormats([]ytDLPFormat{
		{FormatID: "drm", Width: 3840, Height: 2160, VCodec: "av01", ACodec: "aac", HasDRM: true},
		{FormatID: "silent", Width: 3840, Height: 2160, VCodec: "av01", ACodec: "none"},
	})
	for _, option := range options {
		if strings.Contains(option.Quality, "drm") || strings.Contains(option.Quality, "silent") {
			t.Fatalf("unusable option: %+v", option)
		}
	}
}

func TestQualityOptionsPreserveDifferentFrameRatesAndPortraitResolution(t *testing.T) {
	options := qualityOptionsFromFormats([]ytDLPFormat{
		{FormatID: "p30", Width: 1080, Height: 1920, FPS: 30, VCodec: "avc1", ACodec: "aac", Ext: "mp4"},
		{FormatID: "p60", Width: 1080, Height: 1920, FPS: 60, VCodec: "avc1", ACodec: "aac", Ext: "mp4"},
	})
	count := 0
	for _, option := range options {
		if option.Quality == "p30" || option.Quality == "p60" {
			count++
			if !strings.Contains(option.Label, "1080p") {
				t.Fatalf("portrait resolution mislabeled: %s", option.Label)
			}
		}
	}
	if count != 2 {
		t.Fatalf("expected both frame rates, got %+v", options)
	}
}

func TestRunYTDLPIncludesOutputOnFailure(t *testing.T) {
	binary := writeYTDLPTestScript(t, `
echo "[download] 12.3% of 1.00MiB"
echo "ERROR: requested format is not available" >&2
exit 1
`)

	job := &linkDownloadJob{}
	err := runYTDLP(context.Background(), binary, []string{"https://example.com/video"}, job)
	if err == nil {
		t.Fatal("expected yt-dlp failure")
	}

	if !strings.Contains(err.Error(), "yt-dlp failed") {
		t.Fatalf("expected yt-dlp failure prefix, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "ERROR: requested format is not available") {
		t.Fatalf("expected yt-dlp output in error, got %q", err.Error())
	}
	if strings.Contains(err.Error(), "12.3%") {
		t.Fatalf("expected progress output to be omitted, got %q", err.Error())
	}
}

func TestUpdateYTDLPReturnsOutputAndVersion(t *testing.T) {
	binary := writeYTDLPTestScript(t, `
case "$1" in
  -U)
    echo "yt-dlp is up to date"
    ;;
  --version)
    echo "2099.01.01"
    ;;
  *)
    exit 2
    ;;
esac
`)

	result, err := updateYTDLP(context.Background(), binary)
	if err != nil {
		t.Fatalf("expected successful update, got %v", err)
	}

	if result.Version != "2099.01.01" {
		t.Fatalf("version = %q, want 2099.01.01", result.Version)
	}
	if result.Output != "yt-dlp is up to date" {
		t.Fatalf("output = %q, want update output", result.Output)
	}
}

func TestUpdateYTDLPIncludesOutputOnFailure(t *testing.T) {
	binary := writeYTDLPTestScript(t, `
case "$1" in
  -U)
    echo "ERROR: installed yt-dlp cannot self-update" >&2
    exit 1
    ;;
  --version)
    echo "2099.01.01"
    ;;
esac
`)

	_, err := updateYTDLP(context.Background(), binary)
	if err == nil {
		t.Fatal("expected update failure")
	}

	if !strings.Contains(err.Error(), "yt-dlp update failed") {
		t.Fatalf("expected update failure prefix, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "ERROR: installed yt-dlp cannot self-update") {
		t.Fatalf("expected update output in error, got %q", err.Error())
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

func writeYTDLPTestScript(t *testing.T, body string) string {
	t.Helper()

	binary := filepath.Join(t.TempDir(), "yt-dlp")
	script := "#!/bin/sh\n" + body
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write fake yt-dlp: %v", err)
	}
	return binary
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

func TestQualityProbeDoesNotBorrowAudioFromLowerMuxedVideo(t *testing.T) {
	binary := writeYTDLPTestScript(t, `echo '{"formats":[{"format_id":"4k","width":3840,"height":2160,"vcodec":"av01","acodec":"none"},{"format_id":"low","width":1280,"height":720,"vcodec":"avc1","acodec":"aac"}]}'`)
	result, err := ytDLPQualityOptions(context.Background(), binary, "https://example.com/video")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Notice, "4K with audio is not available") {
		t.Fatalf("misleading availability: %+v", result)
	}
}

func TestAutoDownloaderCannotBypassStrictQuality(t *testing.T) {
	_, err := normalizeLinkDownloadRequest(linkDownloadRequest{URL: "https://example.com/video", Downloader: "auto", Quality: settings.DefaultLinkDownloadQuality}, settings.LinkDownload{YTDLPPath: "/missing/yt-dlp"})
	if err == nil {
		t.Fatal("auto downloader must not silently bypass requested video quality")
	}
}

func TestQualityOptionsUseReadableCodecFamilies(t *testing.T) {
	options := qualityOptionsFromFormats([]ytDLPFormat{
		{FormatID: "audio", VCodec: "none", ACodec: "mp4a.40.2", Ext: "m4a"},
		{FormatID: "old", Width: 3840, Height: 2160, FPS: 60, VCodec: "VP09.00.51.08", ACodec: "none", Ext: "webm"},
		{FormatID: "preferred", Width: 3840, Height: 2160, FPS: 60, VCodec: "vp09.00.51.08", ACodec: "none", Ext: "mp4"},
	})
	count := 0
	for _, option := range options {
		if strings.HasPrefix(option.Quality, "old+") || strings.HasPrefix(option.Quality, "preferred+") {
			count++
			if !strings.HasSuffix(option.Label, "VP9 (8-bit)") || !strings.Contains(option.Description, "AAC audio") {
				t.Errorf("not human-readable: %+v", option)
			}
			if option.Quality != "preferred+audio" {
				t.Error("must keep the preferred equivalent stream")
			}
		}
	}
	if count != 1 {
		t.Errorf("expected one equivalent codec choice, got %d", count)
	}
}

func TestQualityOptionsRetainCodecProfiles(t *testing.T) {
	options := qualityOptionsFromFormats([]ytDLPFormat{
		{FormatID: "8bit", Width: 3840, Height: 2160, VCodec: "vp09.00.51.08", ACodec: "opus", Ext: "webm"},
		{FormatID: "10bit", Width: 3840, Height: 2160, VCodec: "vp09.02.51.10", ACodec: "opus", Ext: "webm"},
	})
	count := 0
	for _, o := range options {
		if o.Quality == "8bit" || o.Quality == "10bit" {
			if !strings.Contains(o.Label, strings.Replace(o.Quality, "bit", "-bit", 1)) {
				t.Errorf("missing readable bit depth: %s", o.Label)
			}
			count++
		}
	}
	if count != 2 {
		t.Fatalf("profile choices lost: %+v", options)
	}
}
