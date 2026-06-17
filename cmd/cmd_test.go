package cmd

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"

	"github.com/samber/lo"
	"github.com/spf13/cobra"

	"github.com/filebrowser/filebrowser/v2/settings"
)

// TestEnvCollisions ensures that there are no collisions in the produced environment
// variable names for all commands and their flags.
func TestEnvCollisions(t *testing.T) {
	testEnvCollisions(t, rootCmd)
}

func testEnvCollisions(t *testing.T, cmd *cobra.Command) {
	for _, cmd := range cmd.Commands() {
		testEnvCollisions(t, cmd)
	}

	replacements := generateEnvKeyReplacements(cmd)
	envVariables := []string{}

	for i := range replacements {
		if i%2 != 0 {
			envVariables = append(envVariables, replacements[i])
		}
	}

	duplicates := lo.FindDuplicates(envVariables)

	if len(duplicates) > 0 {
		t.Errorf("Found duplicate environment variable keys for command %q: %v", cmd.Name(), duplicates)
	}
}

type fakeVideoAvailability struct {
	err error
}

func (f fakeVideoAvailability) Available() error {
	return f.err
}

func TestWarnIfVideoThumbnailsUnavailable(t *testing.T) {
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(previous)
	})

	warnIfVideoThumbnailsUnavailable(&settings.Server{EnableThumbnails: false}, fakeVideoAvailability{err: errors.New("missing ffmpeg")})
	if buf.Len() != 0 {
		t.Fatalf("expected no warning when thumbnails are disabled, got %q", buf.String())
	}

	warnIfVideoThumbnailsUnavailable(&settings.Server{EnableThumbnails: true}, fakeVideoAvailability{})
	if buf.Len() != 0 {
		t.Fatalf("expected no warning when service is available, got %q", buf.String())
	}

	warnIfVideoThumbnailsUnavailable(&settings.Server{EnableThumbnails: true}, fakeVideoAvailability{err: errors.New("missing ffmpeg")})
	if !strings.Contains(buf.String(), "Video thumbnails unavailable") || !strings.Contains(buf.String(), "missing ffmpeg") {
		t.Fatalf("expected missing-tool warning, got %q", buf.String())
	}
}
