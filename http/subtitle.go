package fbhttp

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/asticode/go-astisub"

	"github.com/filebrowser/filebrowser/v2/files"
)

var srtLineBreakTag = regexp.MustCompile(`(?i)<br(?:\s+[^>]*)?\s*/?>`)

var subtitleHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	if !d.user.Perm.Download {
		return http.StatusAccepted, nil
	}

	file, err := files.NewFileInfo(&files.FileOptions{
		Fs:         d.user.Fs,
		Path:       r.URL.Path,
		Modify:     d.user.Perm.Modify,
		Expand:     false,
		ReadHeader: d.server.TypeDetectionByHeader,
		Checker:    d,
	})
	if err != nil {
		return errToStatus(err), err
	}

	if file.IsDir {
		return http.StatusBadRequest, nil
	}

	return subtitleFileHandler(w, r, file)
})

func subtitleFileHandler(w http.ResponseWriter, r *http.Request, file *files.FileInfo) (int, error) {
	// if its not a subtitle file, reject
	if !files.IsSupportedSubtitle(file.Name) {
		return http.StatusBadRequest, nil
	}

	// GHSA-448h-jr2h-3vhp: refuse oversized inputs before opening them. The
	// stat size is checked first so a large file is rejected without any read;
	// the bounded read below is defense in depth against a size that changed
	// after stat.
	if file.Size > SubtitleMaxBytes {
		return http.StatusRequestEntityTooLarge, errSubtitleTooLarge
	}

	fd, err := file.Fs.Open(file.Path)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	defer fd.Close()

	// Bound the bytes handed to the parser: at most limit+1 is read, and any
	// excess is rejected instead of being parsed. In particular the SSA/ASS
	// branch no longer passes the raw descriptor to an unbounded parser.
	content, readErr := io.ReadAll(io.LimitReader(fd, SubtitleMaxBytes+1))
	if readErr != nil {
		return http.StatusInternalServerError, readErr
	}
	if int64(len(content)) > SubtitleMaxBytes {
		return http.StatusRequestEntityTooLarge, errSubtitleTooLarge
	}

	// load subtitle for conversion to vtt
	var sub *astisub.Subtitles
	if strings.HasSuffix(file.Name, ".srt") {
		sub, err = astisub.ReadFromSRT(bytes.NewReader(normalizeSRTLineBreaks(content)))
	} else if strings.HasSuffix(file.Name, ".ass") || strings.HasSuffix(file.Name, ".ssa") {
		sub, err = astisub.ReadFromSSA(bytes.NewReader(content))
	}
	if err != nil {
		return http.StatusInternalServerError, err
	}

	setContentDisposition(w, r, file)
	w.Header().Add("Content-Security-Policy", `script-src 'none';`)
	w.Header().Set("Cache-Control", "private")
	// force type to text/vtt
	w.Header().Set("Content-Type", "text/vtt")

	// serve vtt file directly
	if sub == nil {
		http.ServeContent(w, r, file.Name, file.ModTime, fd)
		return 0, nil
	}

	// convert others to vtt and serve from buffer, capping the rendered
	// output so a hostile input cannot expand without bound.
	var buf = &bytes.Buffer{}
	err = sub.WriteToWebVTT(&cappedWriter{w: buf, limit: SubtitleMaxOutputBytes})
	if errors.Is(err, errSubtitleOutputTooLarge) {
		return http.StatusRequestEntityTooLarge, err
	}
	if err != nil {
		return http.StatusInternalServerError, err
	}
	http.ServeContent(w, r, file.Name, file.ModTime, bytes.NewReader(buf.Bytes()))
	return 0, nil
}

func normalizeSRTLineBreaks(content []byte) []byte {
	return srtLineBreakTag.ReplaceAll(content, []byte("\n"))
}

// cappedWriter fails writes once more than limit bytes have been accepted, so
// a conversion whose output expands without bound is rejected with 413
// instead of filling memory.
type cappedWriter struct {
	w     io.Writer
	wrote int64
	limit int64
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.wrote+int64(len(p)) > c.limit {
		return 0, errSubtitleOutputTooLarge
	}
	n, err := c.w.Write(p)
	c.wrote += int64(n)
	return n, err
}
