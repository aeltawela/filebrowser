package fbhttp

import (
	"mime"
	"net/http"
	"net/url"

	"github.com/filebrowser/filebrowser/v2/files"
)

var htmlPreviewHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	if !d.settings.HTMLPreview {
		return http.StatusForbidden, nil
	}

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

	if file.IsDir || files.IsNamedPipe(file.Mode) {
		return http.StatusNotFound, nil
	}

	fd, err := file.Fs.Open(file.Path)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	defer fd.Close()

	w.Header().Set("Content-Disposition", "inline; filename*=utf-8''"+url.PathEscape(file.Name))
	w.Header().Set("Content-Security-Policy", `sandbox allow-scripts allow-same-origin allow-forms allow-modals allow-popups allow-downloads`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private")
	if contentType := mime.TypeByExtension(file.Extension); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(w, r, file.Name, file.ModTime, fd)
	return 0, nil
})
