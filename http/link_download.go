package fbhttp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	fberrors "github.com/filebrowser/filebrowser/v2/errors"
	"github.com/filebrowser/filebrowser/v2/settings"
	"github.com/gorilla/mux"
)

const (
	linkDownloadStatusQueued    = "queued"
	linkDownloadStatusRunning   = "running"
	linkDownloadStatusCompleted = "completed"
	linkDownloadStatusFailed    = "failed"
	linkDownloadStatusCanceled  = "canceled"

	linkDownloaderAuto   = "auto"
	linkDownloaderDirect = "direct"
	linkDownloaderYTDLP  = "yt-dlp"

	maxLinkDownloadBodySize = 1 << 20 // 1 MiB
)

var ytDLPProgressPattern = regexp.MustCompile(`(\d+(?:\.\d+)?)%`)

type linkDownloadRequest struct {
	URL        string `json:"url"`
	Path       string `json:"path"`
	Filename   string `json:"filename"`
	Quality    string `json:"quality"`
	Downloader string `json:"downloader"`
	Overwrite  bool   `json:"overwrite"`
}

type linkDownloadSettingsData struct {
	Enabled        bool   `json:"enabled"`
	DefaultPath    string `json:"defaultPath"`
	DefaultQuality string `json:"defaultQuality"`
	Downloader     string `json:"downloader"`
	YTDLPAvailable bool   `json:"ytdlpAvailable"`
}

type linkDownloadQualityData struct {
	Label   string `json:"label"`
	Quality string `json:"quality"`
}

type linkDownloadQualitiesData struct {
	Downloader string                    `json:"downloader"`
	Options    []linkDownloadQualityData `json:"options"`
	Error      string                    `json:"error,omitempty"`
}

type linkDownloadJobData struct {
	ID            string    `json:"id"`
	URL           string    `json:"url"`
	Path          string    `json:"path"`
	Name          string    `json:"name"`
	Quality       string    `json:"quality"`
	Downloader    string    `json:"downloader"`
	Status        string    `json:"status"`
	Error         string    `json:"error,omitempty"`
	Progress      float64   `json:"progress"`
	BytesReceived int64     `json:"bytesReceived"`
	BytesTotal    int64     `json:"bytesTotal"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt,omitempty"`
}

type linkDownloadJob struct {
	mu            sync.Mutex
	cancel        context.CancelFunc
	ownerID       uint
	id            string
	rawURL        string
	targetDir     string
	name          string
	quality       string
	downloader    string
	status        string
	err           string
	progress      float64
	bytesReceived int64
	bytesTotal    int64
	startedAt     time.Time
	finishedAt    time.Time
}

type linkDownloadManager struct {
	mu     sync.Mutex
	jobs   map[string]*linkDownloadJob
	client *http.Client
}

func newLinkDownloadManager() *linkDownloadManager {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	return &linkDownloadManager{
		jobs: map[string]*linkDownloadJob{},
		client: &http.Client{
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				DialContext:         dialer.DialContext,
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
	}
}

func linkDownloadSettingsHandler(_ *linkDownloadManager) handleFunc {
	return withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
		cfg := d.settings.LinkDownload
		cfg.ApplyDefaults()

		return renderJSON(w, r, linkDownloadSettingsData{
			Enabled:        cfg.Enabled,
			DefaultPath:    cfg.DefaultPath,
			DefaultQuality: cfg.DefaultQuality,
			Downloader:     cfg.Downloader,
			YTDLPAvailable: ytDLPAvailable(cfg.YTDLPPath),
		})
	})
}

func linkDownloadListHandler(manager *linkDownloadManager) handleFunc {
	return withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
		return renderJSON(w, r, manager.list(d.user.ID, d.user.Perm.Admin))
	})
}

func linkDownloadQualitiesHandler(_ *linkDownloadManager) handleFunc {
	return withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
		if !d.settings.LinkDownload.Enabled {
			return http.StatusForbidden, nil
		}

		if !d.user.Perm.Create {
			return http.StatusForbidden, nil
		}

		cfg := d.settings.LinkDownload
		cfg.ApplyDefaults()

		rawURL := strings.TrimSpace(r.URL.Query().Get("url"))
		requestedDownloader := firstNonEmpty(r.URL.Query().Get("downloader"), cfg.Downloader, settings.DefaultLinkDownloadDownloader)
		switch requestedDownloader {
		case linkDownloaderAuto, linkDownloaderDirect, linkDownloaderYTDLP:
		default:
			return http.StatusBadRequest, fmt.Errorf("unsupported downloader %q", requestedDownloader)
		}

		if rawURL == "" {
			return renderJSON(w, r, linkDownloadQualitiesData{
				Downloader: requestedDownloader,
				Options:    defaultLinkDownloadQualities(requestedDownloader),
			})
		}

		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return http.StatusBadRequest, fmt.Errorf("download URL must be http or https: %w", fberrors.ErrInvalidRequestParams)
		}

		downloader, err := resolveLinkDownloader(requestedDownloader, cfg.YTDLPPath)
		if err != nil {
			return renderJSON(w, r, linkDownloadQualitiesData{
				Downloader: requestedDownloader,
				Options:    defaultLinkDownloadQualities(requestedDownloader),
				Error:      err.Error(),
			})
		}

		if downloader != linkDownloaderYTDLP {
			return renderJSON(w, r, linkDownloadQualitiesData{
				Downloader: downloader,
				Options:    defaultLinkDownloadQualities(downloader),
			})
		}

		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()

		options, err := ytDLPQualityOptions(ctx, cfg.YTDLPPath, rawURL)
		if err != nil {
			return renderJSON(w, r, linkDownloadQualitiesData{
				Downloader: downloader,
				Options:    defaultLinkDownloadQualities(downloader),
				Error:      err.Error(),
			})
		}

		return renderJSON(w, r, linkDownloadQualitiesData{
			Downloader: downloader,
			Options:    options,
		})
	})
}

func linkDownloadGetHandler(manager *linkDownloadManager) handleFunc {
	return withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
		job, ok := manager.get(mux.Vars(r)["id"], d.user.ID, d.user.Perm.Admin)
		if !ok {
			return http.StatusNotFound, nil
		}

		return renderJSON(w, r, job.snapshot())
	})
}

func linkDownloadDeleteHandler(manager *linkDownloadManager) handleFunc {
	return withUser(func(_ http.ResponseWriter, r *http.Request, d *data) (int, error) {
		job, ok := manager.get(mux.Vars(r)["id"], d.user.ID, d.user.Perm.Admin)
		if !ok {
			return http.StatusNotFound, nil
		}

		job.cancelJob()
		return http.StatusNoContent, nil
	})
}

func linkDownloadPostHandler(manager *linkDownloadManager) handleFunc {
	return withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
		if !d.settings.LinkDownload.Enabled {
			return http.StatusForbidden, nil
		}

		if !d.user.Perm.Create {
			return http.StatusForbidden, nil
		}

		if r.Body == nil {
			return http.StatusBadRequest, nil
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxLinkDownloadBodySize)

		req := linkDownloadRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return http.StatusBadRequest, err
		}

		cfg := d.settings.LinkDownload
		cfg.ApplyDefaults()

		normalized, err := normalizeLinkDownloadRequest(req, cfg)
		if err != nil {
			return errToStatus(err), err
		}

		if !d.Check(normalized.Path) {
			return http.StatusForbidden, nil
		}

		if normalized.Filename != "" {
			target := path.Join(normalized.Path, normalized.Filename)
			if !d.Check(target) {
				return http.StatusForbidden, nil
			}
		}

		if err := d.user.Fs.MkdirAll(normalized.Path, d.settings.DirMode); err != nil {
			return errToStatus(err), err
		}

		job := manager.start(d, normalized)
		w.WriteHeader(http.StatusAccepted)
		return renderJSON(w, r, job.snapshot())
	})
}

func normalizeLinkDownloadRequest(req linkDownloadRequest, cfg settings.LinkDownload) (linkDownloadRequest, error) {
	req.URL = strings.TrimSpace(req.URL)
	parsed, err := url.Parse(req.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return req, fmt.Errorf("download URL must be http or https: %w", fberrors.ErrInvalidRequestParams)
	}

	req.Path = cleanLinkDownloadPath(firstNonEmpty(req.Path, cfg.DefaultPath, "/"))

	req.Quality = strings.TrimSpace(firstNonEmpty(req.Quality, cfg.DefaultQuality, settings.DefaultLinkDownloadQuality))
	req.Downloader = strings.TrimSpace(firstNonEmpty(req.Downloader, cfg.Downloader, settings.DefaultLinkDownloadDownloader))
	switch req.Downloader {
	case linkDownloaderAuto, linkDownloaderDirect, linkDownloaderYTDLP:
	default:
		return req, fmt.Errorf("unsupported downloader %q: %w", req.Downloader, fberrors.ErrInvalidRequestParams)
	}

	if req.Filename != "" {
		name, err := cleanLinkDownloadFilename(req.Filename)
		if err != nil {
			return req, err
		}
		req.Filename = name
	}

	return req, nil
}

func cleanLinkDownloadPath(raw string) string {
	cleaned := path.Clean("/" + strings.TrimSpace(raw))
	if cleaned == "." {
		return "/"
	}
	return cleaned
}

func cleanLinkDownloadFilename(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base("/" + name)
	if name == "" || name == "." || name == "/" || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("invalid file name: %w", fberrors.ErrInvalidRequestParams)
	}
	return name, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (m *linkDownloadManager) start(d *data, req linkDownloadRequest) *linkDownloadJob {
	m.prune()

	ctx, cancel := context.WithCancel(context.Background())
	job := &linkDownloadJob{
		cancel:     cancel,
		ownerID:    d.user.ID,
		id:         strconv.FormatInt(time.Now().UnixNano(), 36),
		rawURL:     req.URL,
		targetDir:  req.Path,
		name:       req.Filename,
		quality:    req.Quality,
		downloader: req.Downloader,
		status:     linkDownloadStatusQueued,
		startedAt:  time.Now(),
	}

	m.mu.Lock()
	m.jobs[job.id] = job
	m.mu.Unlock()

	go m.run(ctx, d, job, req)

	return job
}

func (m *linkDownloadManager) run(ctx context.Context, d *data, job *linkDownloadJob, req linkDownloadRequest) {
	job.setStatus(linkDownloadStatusRunning)

	downloader, err := resolveLinkDownloader(req.Downloader, d.settings.LinkDownload.YTDLPPath)
	if err != nil {
		job.fail(err)
		return
	}
	job.setDownloader(downloader)

	if downloader == linkDownloaderYTDLP {
		err = m.downloadWithYTDLP(ctx, d, job, req)
	} else {
		err = m.downloadDirect(ctx, d, job, req)
	}

	if errors.Is(err, context.Canceled) {
		job.cancelled()
		return
	}

	if err != nil {
		job.fail(err)
		return
	}

	job.complete()
}

func resolveLinkDownloader(requested, ytDLPPath string) (string, error) {
	if requested == linkDownloaderYTDLP {
		if !ytDLPAvailable(ytDLPPath) {
			return "", fmt.Errorf("yt-dlp is not available")
		}
		return linkDownloaderYTDLP, nil
	}

	if requested == linkDownloaderAuto && ytDLPAvailable(ytDLPPath) {
		return linkDownloaderYTDLP, nil
	}

	return linkDownloaderDirect, nil
}

func ytDLPAvailable(binary string) bool {
	if strings.TrimSpace(binary) == "" {
		return false
	}
	_, err := exec.LookPath(binary)
	return err == nil
}

func defaultLinkDownloadQualities(downloader string) []linkDownloadQualityData {
	if downloader == linkDownloaderDirect {
		return []linkDownloadQualityData{
			{Label: "Original file", Quality: "best"},
		}
	}

	return []linkDownloadQualityData{
		{Label: "Best available", Quality: settings.DefaultLinkDownloadQuality},
		{Label: "1080p", Quality: heightLimitedFormatSelector(1080)},
		{Label: "720p", Quality: heightLimitedFormatSelector(720)},
		{Label: "Audio only", Quality: "bestaudio/best"},
	}
}

type ytDLPMetadata struct {
	Formats []ytDLPFormat `json:"formats"`
}

type ytDLPFormat struct {
	Height int    `json:"height"`
	VCodec string `json:"vcodec"`
	ACodec string `json:"acodec"`
}

func ytDLPQualityOptions(ctx context.Context, binary, rawURL string) ([]linkDownloadQualityData, error) {
	cmd := exec.CommandContext(ctx, binary, "--dump-single-json", "--no-playlist", "--no-warnings", "--skip-download", rawURL)
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("yt-dlp could not read available qualities")
	}

	metadata := ytDLPMetadata{}
	if err := json.Unmarshal(output, &metadata); err != nil {
		return nil, fmt.Errorf("yt-dlp returned invalid format metadata")
	}

	return qualityOptionsFromFormats(metadata.Formats), nil
}

func qualityOptionsFromFormats(formats []ytDLPFormat) []linkDownloadQualityData {
	options := []linkDownloadQualityData{
		{Label: "Best available", Quality: settings.DefaultLinkDownloadQuality},
	}

	heights := map[int]struct{}{}
	hasAudio := false
	for _, format := range formats {
		if format.ACodec != "" && format.ACodec != "none" {
			hasAudio = true
		}

		if format.Height <= 0 || format.VCodec == "none" {
			continue
		}
		heights[format.Height] = struct{}{}
	}

	sortedHeights := make([]int, 0, len(heights))
	for height := range heights {
		sortedHeights = append(sortedHeights, height)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sortedHeights)))

	for _, height := range sortedHeights {
		options = append(options, linkDownloadQualityData{
			Label:   fmt.Sprintf("%dp", height),
			Quality: heightLimitedFormatSelector(height),
		})
	}

	if hasAudio {
		options = append(options, linkDownloadQualityData{
			Label:   "Audio only",
			Quality: "bestaudio/best",
		})
	}

	return options
}

func heightLimitedFormatSelector(height int) string {
	return fmt.Sprintf("bv*[height<=%d]+ba/b[height<=%d]/wv*+ba/w", height, height)
}

func (m *linkDownloadManager) downloadDirect(ctx context.Context, d *data, job *linkDownloadJob, req linkDownloadRequest) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, http.NoBody)
	if err != nil {
		return err
	}
	httpReq.Header.Set("User-Agent", "File Browser")

	resp, err := m.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode > 299 {
		return fmt.Errorf("remote server returned %s", resp.Status)
	}

	name := req.Filename
	if name == "" {
		name, err = nameFromDownloadResponse(resp, req.URL)
		if err != nil {
			return err
		}
	}

	targetPath := path.Join(req.Path, name)
	if !d.Check(targetPath) {
		return fberrors.ErrPermissionDenied
	}
	job.setName(name)
	job.setTotal(resp.ContentLength)

	return d.RunHook(func() error {
		return m.writeDirectDownload(ctx, d, job, resp.Body, targetPath, req.Overwrite)
	}, "upload", targetPath, "", d.user)
}

func nameFromDownloadResponse(resp *http.Response, rawURL string) (string, error) {
	if disposition := resp.Header.Get("Content-Disposition"); disposition != "" {
		_, params, err := mime.ParseMediaType(disposition)
		if err == nil {
			if filename := params["filename"]; filename != "" {
				return cleanLinkDownloadFilename(filename)
			}
		}
	}

	parsed, err := url.Parse(rawURL)
	if err == nil {
		base := path.Base(parsed.EscapedPath())
		if unescaped, unescapeErr := url.PathUnescape(base); unescapeErr == nil {
			base = unescaped
		}
		if base != "" && base != "." && base != "/" {
			return cleanLinkDownloadFilename(base)
		}
	}

	return "download", nil
}

func (m *linkDownloadManager) writeDirectDownload(ctx context.Context, d *data, job *linkDownloadJob, body io.Reader, targetPath string, overwrite bool) error {
	if exists, err := exists(d.user.Fs, targetPath); err != nil {
		return err
	} else if exists {
		if !overwrite {
			return fberrors.ErrExist
		}
		if !d.user.Perm.Modify {
			return fberrors.ErrPermissionDenied
		}
	}

	targetDir, targetName := path.Split(targetPath)
	tmpPath := path.Join(targetDir, "."+targetName+".filebrowser-download-"+job.id+".tmp")
	_ = d.user.Fs.Remove(tmpPath)
	defer d.user.Fs.Remove(tmpPath)

	file, err := d.user.Fs.OpenFile(tmpPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, d.settings.FileMode)
	if err != nil {
		return err
	}

	reader := &linkDownloadProgressReader{
		ctx: ctx,
		job: job,
		r:   body,
	}
	if _, err = io.Copy(file, reader); err != nil {
		_ = file.Close()
		return err
	}

	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}

	if err = file.Close(); err != nil {
		return err
	}

	if overwrite {
		_ = d.user.Fs.Remove(targetPath)
	}

	return d.user.Fs.Rename(tmpPath, targetPath)
}

func (m *linkDownloadManager) downloadWithYTDLP(ctx context.Context, d *data, job *linkDownloadJob, req linkDownloadRequest) error {
	realDir, err := realDownloadPath(d, req.Path)
	if err != nil {
		return err
	}

	outputTemplate := "%(title).200B.%(ext)s"
	if req.Filename != "" {
		outputTemplate = escapeYTDLPTemplate(req.Filename)
		if filepath.Ext(req.Filename) == "" {
			outputTemplate += ".%(ext)s"
		}
	}

	args := []string{
		"--newline",
		"--no-playlist",
		"-f", req.Quality,
		"-o", filepath.Join(realDir, outputTemplate),
	}

	if req.Overwrite {
		args = append(args, "--force-overwrites")
	} else {
		args = append(args, "--no-overwrites")
	}

	args = append(args, req.URL)

	return d.RunHook(func() error {
		return runYTDLP(ctx, d.settings.LinkDownload.YTDLPPath, args, job)
	}, "upload", req.Path, "", d.user)
}

func realDownloadPath(d *data, scopedPath string) (string, error) {
	if d.user.Fs == nil {
		return scopedPath, nil
	}

	return d.user.Fs.RealPath(scopedPath)
}

func escapeYTDLPTemplate(name string) string {
	return strings.ReplaceAll(name, "%", "%%")
}

func runYTDLP(ctx context.Context, binary string, args []string, job *linkDownloadJob) error {
	cmd := exec.CommandContext(ctx, binary, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err = cmd.Start(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	scan := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			job.observeYTDLPLine(scanner.Text())
		}
	}

	wg.Add(2)
	go scan(stdout)
	go scan(stderr)

	err = cmd.Wait()
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("yt-dlp failed: %w", err)
	}

	return nil
}

type linkDownloadProgressReader struct {
	ctx context.Context
	job *linkDownloadJob
	r   io.Reader
}

func (r *linkDownloadProgressReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}

	n, err := r.r.Read(p)
	if n > 0 {
		r.job.addBytes(int64(n))
	}
	return n, err
}

func (m *linkDownloadManager) get(id string, userID uint, admin bool) (*linkDownloadJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs[id]
	if !ok {
		return nil, false
	}

	if !admin && job.ownerID != userID {
		return nil, false
	}

	return job, true
}

func (m *linkDownloadManager) list(userID uint, admin bool) []linkDownloadJobData {
	m.mu.Lock()
	defer m.mu.Unlock()

	jobs := make([]linkDownloadJobData, 0, len(m.jobs))
	for _, job := range m.jobs {
		if !admin && job.ownerID != userID {
			continue
		}
		jobs = append(jobs, job.snapshot())
	}
	return jobs
}

func (m *linkDownloadManager) prune() {
	cutoff := time.Now().Add(-24 * time.Hour)
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, job := range m.jobs {
		snapshot := job.snapshot()
		if snapshot.FinishedAt.IsZero() || snapshot.FinishedAt.After(cutoff) {
			continue
		}
		delete(m.jobs, id)
	}
}

func (j *linkDownloadJob) snapshot() linkDownloadJobData {
	j.mu.Lock()
	defer j.mu.Unlock()

	return linkDownloadJobData{
		ID:            j.id,
		URL:           j.rawURL,
		Path:          j.targetDir,
		Name:          j.name,
		Quality:       j.quality,
		Downloader:    j.downloader,
		Status:        j.status,
		Error:         j.err,
		Progress:      j.progress,
		BytesReceived: j.bytesReceived,
		BytesTotal:    j.bytesTotal,
		StartedAt:     j.startedAt,
		FinishedAt:    j.finishedAt,
	}
}

func (j *linkDownloadJob) setStatus(status string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = status
}

func (j *linkDownloadJob) setDownloader(downloader string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.downloader = downloader
}

func (j *linkDownloadJob) setName(name string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.name = name
}

func (j *linkDownloadJob) setTotal(total int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.bytesTotal = total
}

func (j *linkDownloadJob) addBytes(n int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.bytesReceived += n
	if j.bytesTotal > 0 {
		j.progress = float64(j.bytesReceived) / float64(j.bytesTotal) * 100
	}
}

func (j *linkDownloadJob) observeYTDLPLine(line string) {
	if match := ytDLPProgressPattern.FindStringSubmatch(line); len(match) == 2 {
		if percent, err := strconv.ParseFloat(match[1], 64); err == nil {
			j.mu.Lock()
			j.progress = percent
			j.mu.Unlock()
		}
	}

	const destinationPrefix = "[download] Destination: "
	if strings.Contains(line, destinationPrefix) {
		parts := strings.SplitN(line, destinationPrefix, 2)
		if len(parts) == 2 {
			j.setName(filepath.Base(strings.TrimSpace(parts[1])))
		}
	}
}

func (j *linkDownloadJob) complete() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = linkDownloadStatusCompleted
	j.progress = 100
	j.finishedAt = time.Now()
}

func (j *linkDownloadJob) fail(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = linkDownloadStatusFailed
	j.err = err.Error()
	j.finishedAt = time.Now()
}

func (j *linkDownloadJob) cancelled() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = linkDownloadStatusCanceled
	j.finishedAt = time.Now()
}

func (j *linkDownloadJob) cancelJob() {
	j.mu.Lock()
	terminal := j.status == linkDownloadStatusCompleted || j.status == linkDownloadStatusFailed || j.status == linkDownloadStatusCanceled
	cancel := j.cancel
	j.mu.Unlock()
	if !terminal && cancel != nil {
		cancel()
	}
}

func exists(afs interface {
	Stat(name string) (os.FileInfo, error)
}, name string) (bool, error) {
	_, err := afs.Stat(name)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
