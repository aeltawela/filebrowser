package thumbnail

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestVideoThumbnailUnavailable(t *testing.T) {
	err := NewVideoWithTools("", "").Thumbnail(context.Background(), "video.mp4", &bytes.Buffer{})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestVideoThumbnailUsesFfmpegOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test helper is unix-only")
	}

	dir := t.TempDir()
	argsFile := filepath.Join(dir, "ffmpeg.args")
	ffprobe := writeScript(t, dir, "ffprobe", "printf '12.5\\n'\n")
	ffmpeg := writeScript(t, dir, "ffmpeg", "printf '%s\\n' \"$@\" > \"$FFMPEG_ARGS_FILE\"\nprintf 'jpeg-bytes'\n")

	t.Setenv("FFMPEG_ARGS_FILE", argsFile)
	buf := &bytes.Buffer{}
	err := NewVideoWithTools(ffmpeg, ffprobe).Thumbnail(context.Background(), filepath.Join(dir, "video.mp4"), buf)
	if err != nil {
		t.Fatal(err)
	}

	if got := buf.String(); got != "jpeg-bytes" {
		t.Fatalf("got output %q", got)
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	gotArgs := string(args)
	for _, want := range []string{"-nostdin", "-threads", "1", "-ss", "1.250", "-map", "0:v:0", "-frames:v", "1", "-an", "-sn", "pipe:1"} {
		if !strings.Contains(gotArgs, want) {
			t.Fatalf("expected ffmpeg args to contain %q, got:\n%s", want, gotArgs)
		}
	}
}

func TestVideoThumbnailTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test helper is unix-only")
	}

	dir := t.TempDir()
	ffprobe := writeScript(t, dir, "ffprobe", "printf '12.5\\n'\n")
	ffmpeg := writeScript(t, dir, "ffmpeg", "sleep 2\nprintf 'jpeg-bytes'\n")

	err := NewVideoWithLimits(ffmpeg, ffprobe, 1, 10*time.Millisecond).Thumbnail(context.Background(), filepath.Join(dir, "video.mp4"), &bytes.Buffer{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
}

func TestVideoThumbnailFallsBackWhenDurationIsUnavailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test helper is unix-only")
	}

	dir := t.TempDir()
	ffprobe := writeScript(t, dir, "ffprobe", "printf 'N/A\\n'\n")
	ffmpeg := writeScript(t, dir, "ffmpeg", "printf 'jpeg-bytes'\n")

	buf := &bytes.Buffer{}
	err := NewVideoWithTools(ffmpeg, ffprobe).Thumbnail(context.Background(), filepath.Join(dir, "video.mp4"), buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "jpeg-bytes" {
		t.Fatalf("got output %q", got)
	}
}

func TestSeekOffset(t *testing.T) {
	tests := map[float64]string{
		0:     "0.100",
		0.5:   "0.250",
		12.5:  "1.250",
		300.0: "10.000",
	}

	for duration, want := range tests {
		if got := seekOffset(duration); got != want {
			t.Fatalf("seekOffset(%f) = %s, want %s", duration, got, want)
		}
	}
}

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	content := "#!/bin/sh\nset -eu\n" + body
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
