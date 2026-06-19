package thumbnail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/marusama/semaphore/v2"
)

var ErrUnavailable = errors.New("thumbnail generator unavailable")

const (
	DefaultVideoWorkers = 1
	DefaultVideoTimeout = 30 * time.Second
)

type Video struct {
	ffmpeg  string
	ffprobe string
	sem     semaphore.Semaphore
	timeout time.Duration
}

func NewVideo() *Video {
	return NewVideoWithTools(FindFFmpeg(), FindFFprobe())
}

func FindFFmpeg() string {
	ffmpeg, _ := exec.LookPath("ffmpeg")
	return ffmpeg
}

func FindFFprobe() string {
	ffprobe, _ := exec.LookPath("ffprobe")
	return ffprobe
}

func NewVideoWithTools(ffmpeg, ffprobe string) *Video {
	return NewVideoWithLimits(ffmpeg, ffprobe, DefaultVideoWorkers, DefaultVideoTimeout)
}

func NewVideoWithLimits(ffmpeg, ffprobe string, workers int, timeout time.Duration) *Video {
	if workers < 1 {
		workers = DefaultVideoWorkers
	}
	if timeout <= 0 {
		timeout = DefaultVideoTimeout
	}

	return &Video{
		ffmpeg:  ffmpeg,
		ffprobe: ffprobe,
		sem:     semaphore.New(workers),
		timeout: timeout,
	}
}

func (v *Video) Available() error {
	if v == nil {
		return fmt.Errorf("%w: missing ffmpeg and ffprobe", ErrUnavailable)
	}

	missing := []string{}
	if v.ffmpeg == "" {
		missing = append(missing, "ffmpeg")
	}
	if v.ffprobe == "" {
		missing = append(missing, "ffprobe")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: missing %s", ErrUnavailable, strings.Join(missing, " and "))
	}
	if v.sem == nil {
		return fmt.Errorf("%w: worker limiter is not configured", ErrUnavailable)
	}

	return nil
}

func (v *Video) Thumbnail(ctx context.Context, input string, out io.Writer) error {
	if err := v.Available(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	if err := v.sem.Acquire(ctx, 1); err != nil {
		return err
	}
	defer v.sem.Release(1)

	duration, err := v.duration(ctx, input)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		duration = 0
	}

	args := []string{
		"-nostdin",
		"-hide_banner",
		"-loglevel", "error",
		"-threads", "1",
		"-ss", seekOffset(duration),
		"-i", input,
		"-map", "0:v:0",
		"-frames:v", "1",
		"-vf", "scale=256:256:force_original_aspect_ratio=increase,crop=256:256",
		"-an",
		"-sn",
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"pipe:1",
	}

	cmd := exec.CommandContext(ctx, v.ffmpeg, args...)
	cmd.Stdout = out
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	_, readErr := io.ReadAll(stderr)
	waitErr := cmd.Wait()
	if waitErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("ffmpeg thumbnail failed: %w", waitErr)
	}

	return readErr
}

func (v *Video) duration(ctx context.Context, input string) (float64, error) {
	args := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		input,
	}

	out, err := exec.CommandContext(ctx, v.ffprobe, args...).Output()
	if err != nil {
		return 0, err
	}

	duration, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, err
	}
	return duration, nil
}

func seekOffset(duration float64) string {
	if duration <= 0 {
		return "0.100"
	}
	if duration < 1 {
		return fmt.Sprintf("%.3f", duration/2)
	}

	seek := duration * 0.1
	if seek < 0.1 {
		seek = 0.1
	}
	if seek > 10 {
		seek = 10
	}
	if seek >= duration {
		seek = duration / 2
	}

	return fmt.Sprintf("%.3f", seek)
}
