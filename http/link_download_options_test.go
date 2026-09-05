package fbhttp

import (
	"encoding/json"
	"github.com/filebrowser/filebrowser/v2/settings"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDownloadMediaOptionValidation(t *testing.T) {
	for _, body := range []string{
		`{"container":"avi"}`, `{"subtitleLanguage":"en.*"}`, `{"subtitleLanguage":"--help"}`,
		`{"container":"mp4","downloader":"direct"}`, `{"subtitleLanguage":"en","downloader":"direct"}`,
	} {
		t.Run(body, func(t *testing.T) {
			req := linkDownloadRequest{URL: "https://example.com/video", Quality: "best"}
			if err := json.Unmarshal([]byte(body), &req); err != nil {
				t.Fatal(err)
			}
			if _, err := normalizeLinkDownloadRequest(req, settings.LinkDownload{YTDLPPath: "sh"}); err == nil {
				t.Fatal("expected invalid media options to be rejected")
			}
		})
	}
}

func TestDownloadMediaArguments(t *testing.T) {
	tests := []struct {
		name, container, language string
		extra                     []string
	}{
		{name: "default"},
		{name: "mkv", container: "mkv", extra: []string{"--remux-video", "mkv"}},
		{name: "mp4", container: "mp4", extra: []string{"--remux-video", "mp4"}},
		{name: "subtitles", language: "pt-BR", extra: []string{"--write-subs", "--write-auto-subs", "--sub-langs", "^pt-BR$", "--embed-subs"}},
		{name: "mp4 subtitles", container: "mp4", language: "en", extra: []string{"--remux-video", "mp4", "--write-subs", "--write-auto-subs", "--sub-langs", "^en$", "--embed-subs"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := linkDownloadRequest{URL: "https://example.com/video", Quality: "bestvideo+bestaudio", Container: tt.container, SubtitleLanguage: tt.language, Filename: "sample"}
			container := tt.container
			if container == "" {
				container = "mkv"
			}
			want := append(ytDLPBaseArgs(), "--newline", "--no-playlist", "-f", req.Quality, "--merge-output-format", container, "-o", filepath.Join("downloads", "sample.%(ext)s"))
			want = append(want, tt.extra...)
			want = append(want, "--no-overwrites", req.URL)
			if got := ytDLPDownloadArgs(req, "downloads"); !reflect.DeepEqual(got, want) {
				t.Fatalf("arguments = %q, want %q", got, want)
			}
		})
	}
}

func TestDownloadSubtitleLanguageBounds(t *testing.T) {
	for _, language := range []string{"en", "pt-BR", "en_orig", strings.Repeat("a", 35)} {
		if err := validateLinkDownloadMediaOptions(linkDownloadRequest{SubtitleLanguage: language}); err != nil {
			t.Fatal(err)
		}
	}
	for _, language := range []string{strings.Repeat("a", 36), "en,fr", "en\n", "en$", "../en", " en"} {
		if err := validateLinkDownloadMediaOptions(linkDownloadRequest{SubtitleLanguage: language}); err == nil {
			t.Fatalf("accepted %q", language)
		}
	}
}

func TestMediaOptionsCannotFallBackToDirect(t *testing.T) {
	req := linkDownloadRequest{URL: "https://example.com/video", Quality: "best", Container: "mp4", Downloader: linkDownloaderAuto}
	if _, err := normalizeLinkDownloadRequest(req, settings.LinkDownload{YTDLPPath: "missing-downloader-for-test"}); err == nil {
		t.Fatal("media options must require yt-dlp")
	}
}
