package fbhttp

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/files"
)

// GHSA-448h-jr2h-3vhp: subtitle conversion must reject oversized inputs with
// 413 instead of reading them fully into memory. Normal files still convert.
func TestSubtitleSizeLimit(t *testing.T) {
	// A small valid SRT must keep converting (SRT <br> fix intact).
	t.Run("small SRT converts", func(t *testing.T) {
		fs := afero.NewMemMapFs()
		const path = "/sample.srt"
		const content = "1\n00:00:01,000 --> 00:00:02,000\nFirst<br>Second\n\n"
		if err := afero.WriteFile(fs, path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		info, _ := fs.Stat(path)
		file := &files.FileInfo{Fs: fs, Path: path, Name: "sample.srt", Size: info.Size(), ModTime: info.ModTime()}
		req := httptest.NewRequest(http.MethodGet, "/api/subtitle/sample.srt?inline=true", http.NoBody)
		rec := httptest.NewRecorder()
		if status, err := subtitleFileHandler(rec, req, file); err != nil || status != 0 {
			t.Fatalf("small SRT: status=%d err=%v, want conversion to succeed", status, err)
		}
		if !strings.Contains(rec.Body.String(), "First\nSecond") {
			t.Fatalf("small SRT: <br> fix lost, body=%q", rec.Body.String())
		}
	})

	// Oversized inputs must be rejected with 413 before parsing.
	for _, name := range []string{"big.srt", "big.ass"} {
		t.Run("oversized "+name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			const path = "/big"
			var content []byte
			if strings.HasSuffix(name, ".srt") {
				content = []byte(strings.Repeat("1\n00:00:01,000 --> 00:00:02,000\nhello\n\n", 1<<17))
			} else {
				content = []byte("[Script Info]\nTitle: big\n\n[V4+ Styles]\nFormat: Name\nStyle: Default\n\n[Events]\nFormat: Layer, Start, End, Style, Text\n" +
					strings.Repeat("Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,hello\n", 1<<17))
			}
			if int64(len(content)) <= SubtitleMaxBytes {
				t.Fatalf("test setup: content %d bytes does not exceed limit %d", len(content), SubtitleMaxBytes)
			}
			if err := afero.WriteFile(fs, path, content, 0o644); err != nil {
				t.Fatal(err)
			}
			info, _ := fs.Stat(path)
			file := &files.FileInfo{Fs: fs, Path: path, Name: name, Size: info.Size(), ModTime: info.ModTime()}
			req := httptest.NewRequest(http.MethodGet, "/api/subtitle/"+name+"?inline=true", http.NoBody)
			rec := httptest.NewRecorder()
			status, _ := subtitleFileHandler(rec, req, file)
			if status != http.StatusRequestEntityTooLarge {
				t.Fatalf("VULNERABLE: oversized %s: status=%d, want 413", name, status)
			}
		})
	}
}

// The WebVTT output cap is the second half of the GHSA-448h-jr2h-3vhp bound:
// conversion output that expands without bound must be rejected instead of
// filling memory, even when the input itself passed the input cap.
func TestSubtitleOutputCap(t *testing.T) {
	var buf bytes.Buffer
	c := &cappedWriter{w: &buf, limit: 8}
	if _, err := c.Write([]byte("12345678")); err != nil {
		t.Fatalf("write within the cap failed: %v", err)
	}
	if _, err := c.Write([]byte("x")); err == nil {
		t.Fatal("VULNERABLE: write past the output cap was accepted")
	}
}
