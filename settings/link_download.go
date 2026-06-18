package settings

const (
	DefaultLinkDownloadQuality    = "bestvideo*+bestaudio/best"
	DefaultLinkDownloadDownloader = "auto"
	DefaultLinkDownloadYTDLPPath  = "yt-dlp"
)

// LinkDownload stores defaults for server-side downloads started from a URL.
type LinkDownload struct {
	Enabled        bool   `json:"enabled"`
	DefaultPath    string `json:"defaultPath"`
	DefaultQuality string `json:"defaultQuality"`
	Downloader     string `json:"downloader"`
	YTDLPPath      string `json:"ytdlpPath"`
}

// ApplyDefaults fills missing fields without changing the enabled state.
func (l *LinkDownload) ApplyDefaults() {
	if l.DefaultQuality == "" || l.DefaultQuality == "best" {
		l.DefaultQuality = DefaultLinkDownloadQuality
	}

	if l.Downloader == "" {
		l.Downloader = DefaultLinkDownloadDownloader
	}

	if l.YTDLPPath == "" {
		l.YTDLPPath = DefaultLinkDownloadYTDLPPath
	}
}
