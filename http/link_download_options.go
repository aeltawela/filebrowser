package fbhttp

import (
	"fmt"
	"path/filepath"
	"regexp"

	fberrors "github.com/filebrowser/filebrowser/v2/errors"
)

var subtitleLanguagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,34}$`)

func validateLinkDownloadMediaOptions(req linkDownloadRequest) error {
	switch req.Container {
	case "", "mkv", "mp4":
	default:
		return fmt.Errorf("unsupported output container: %w", fberrors.ErrInvalidRequestParams)
	}
	if req.SubtitleLanguage != "" && !subtitleLanguagePattern.MatchString(req.SubtitleLanguage) {
		return fmt.Errorf("invalid subtitle language: %w", fberrors.ErrInvalidRequestParams)
	}
	if req.Downloader == linkDownloaderDirect && (req.Container != "" || req.SubtitleLanguage != "") {
		return fmt.Errorf("direct downloads cannot change containers or embed subtitles: %w", fberrors.ErrInvalidRequestParams)
	}
	return nil
}

func ytDLPDownloadArgs(req linkDownloadRequest, realDir string) []string {
	outputTemplate := "%(title).200B.%(ext)s"
	if req.Filename != "" {
		outputTemplate = escapeYTDLPTemplate(req.Filename)
		if filepath.Ext(req.Filename) == "" {
			outputTemplate += ".%(ext)s"
		}
	}
	container := req.Container
	if container == "" {
		container = "mkv"
	}
	args := append(ytDLPBaseArgs(), "--newline", "--no-playlist", "-f", req.Quality,
		"--merge-output-format", container, "-o", filepath.Join(realDir, outputTemplate))
	if req.Container != "" {
		args = append(args, "--remux-video", req.Container)
	}
	if req.SubtitleLanguage != "" {
		args = append(args, "--write-subs", "--write-auto-subs", "--sub-langs", "^"+req.SubtitleLanguage+"$", "--embed-subs")
	}
	if req.Overwrite {
		args = append(args, "--force-overwrites")
	} else {
		args = append(args, "--no-overwrites")
	}
	return append(args, req.URL)
}
