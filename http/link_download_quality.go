package fbhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/filebrowser/filebrowser/v2/settings"
)

const highestAvailableQuality = "bv*+ba/b"

var ytDLPFormatIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

func defaultLinkDownloadQualities(downloader string) []linkDownloadQualityData {
	if downloader == linkDownloaderDirect {
		return []linkDownloadQualityData{{Label: "Original file", Quality: "best", Description: "Downloads the original file; its quality cannot be changed."}}
	}
	return []linkDownloadQualityData{
		{Label: "4K or better · video + audio", Quality: settings.DefaultLinkDownloadQuality, Description: "Requires at least 2160 pixels on both dimensions. Downloads separate video and audio when needed and merges without reducing quality. Fails if 4K is unavailable; never silently downloads a lower resolution."},
		{Label: "Highest available · any resolution", Quality: highestAvailableQuality, Description: "May be below 4K. Downloads the best available video with audio."},
		{Label: "Audio only", Quality: "bestaudio/best", Description: "Downloads audio without video when a separate audio stream is available."},
	}
}

type ytDLPMetadata struct {
	Formats []ytDLPFormat `json:"formats"`
}
type ytDLPFormat struct {
	FormatID       string  `json:"format_id"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	FPS            float64 `json:"fps"`
	DynamicRange   string  `json:"dynamic_range"`
	VCodec         string  `json:"vcodec"`
	ACodec         string  `json:"acodec"`
	Ext            string  `json:"ext"`
	Filesize       float64 `json:"filesize"`
	FilesizeApprox float64 `json:"filesize_approx"`
	HasDRM         bool    `json:"has_drm"`
}

// Use the same explicit sorting and configuration for discovery and downloads.
func ytDLPBaseArgs() []string {
	return []string{"--ignore-config", "--no-update", "--no-remote-components", "--format-sort-force", "-S", "res,fps,hdr:12"}
}

func ytDLPQualityOptions(ctx context.Context, binary, rawURL string) (linkDownloadQualitiesData, error) {
	args := append(ytDLPBaseArgs(), "--dump-single-json", "--no-playlist", "--skip-download", "--", rawURL)
	cmd := exec.CommandContext(ctx, binary, args...)
	var warnings bytes.Buffer
	cmd.Stderr = &warnings
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return linkDownloadQualitiesData{}, ctx.Err()
	}
	if err != nil {
		return linkDownloadQualitiesData{}, fmt.Errorf("could not verify source qualities; retry or choose an unverified preset")
	}
	var metadata ytDLPMetadata
	if err := json.Unmarshal(output, &metadata); err != nil {
		return linkDownloadQualitiesData{}, fmt.Errorf("yt-dlp returned invalid format metadata")
	}
	result := linkDownloadQualitiesData{Options: qualityOptionsFromFormats(metadata.Formats), Verified: true}
	has4K := false
	hasSeparateAudio := false
	for _, f := range metadata.Formats {
		if usableFormat(f) && f.VCodec == "none" && f.ACodec != "" && f.ACodec != "none" {
			hasSeparateAudio = true
		}
	}
	for _, f := range metadata.Formats {
		if usableVideoFormat(f) && f.Width >= 2160 && f.Height >= 2160 && ((f.ACodec != "" && f.ACodec != "none") || hasSeparateAudio) {
			has4K = true
		}
	}
	if !has4K {
		result.Notice = "4K with audio is not available from this source. Choose a lower quality explicitly, or use another source. The 4K default will not downgrade."
	}
	if warnings.Len() > 0 {
		result.Notice = strings.TrimSpace(result.Notice + " The source reported extraction warnings; some qualities may be missing. Retry if the expected quality is absent.")
	}
	if len(metadata.Formats) == 0 {
		result.Verified = false
		result.Notice = "The source did not provide format details. These presets are unverified."
	}
	return result, nil
}

func usableFormat(f ytDLPFormat) bool {
	return !f.HasDRM && ytDLPFormatIDPattern.MatchString(f.FormatID)
}
func usableVideoFormat(f ytDLPFormat) bool {
	return usableFormat(f) && f.Width > 0 && f.Height > 0 && f.VCodec != "" && f.VCodec != "none"
}

func qualityOptionsFromFormats(formats []ytDLPFormat) []linkDownloadQualityData {
	options := defaultLinkDownloadQualities(linkDownloaderYTDLP)[:2]
	var audio *ytDLPFormat
	// yt-dlp returns formats from least to most preferred with our shared sort.
	for i := range formats {
		f := &formats[i]
		if usableFormat(*f) && f.VCodec == "none" && f.ACodec != "" && f.ACodec != "none" {
			audio = f
		}
	}
	grouped := map[string]ytDLPFormat{}
	for _, f := range formats {
		if !usableVideoFormat(f) {
			continue
		}
		if (f.ACodec == "none" || f.ACodec == "") && audio == nil {
			continue
		}
		container := f.Ext
		if f.ACodec == "none" || f.ACodec == "" {
			container = "mkv"
		}
		key := fmt.Sprintf("%dx%d/%g/%s/%s/%s", f.Width, f.Height, f.FPS, f.DynamicRange, container, strings.ToLower(f.VCodec))
		grouped[key] = f
	}
	videos := make([]ytDLPFormat, 0, len(grouped))
	for _, f := range grouped {
		videos = append(videos, f)
	}
	sort.Slice(videos, func(i, j int) bool {
		a, b := videos[i], videos[j]
		ar, br := min(a.Width, a.Height), min(b.Width, b.Height)
		if ar != br {
			return ar > br
		}
		if a.FPS != b.FPS {
			return a.FPS > b.FPS
		}
		if a.DynamicRange != b.DynamicRange {
			return a.DynamicRange > b.DynamicRange
		}
		return a.FormatID < b.FormatID
	})
	for _, f := range videos {
		quality := f.FormatID
		size := formatSize(f)
		container := strings.ToUpper(f.Ext)
		description := fmt.Sprintf("%d × %d · %s video", f.Width, f.Height, readableCodec(f.VCodec))
		if f.ACodec == "none" || f.ACodec == "" {
			quality += "+" + audio.FormatID
			container = "MKV"
			description += fmt.Sprintf(" + %s audio; merged without re-encoding (%s source video).", readableCodec(audio.ACodec), strings.ToUpper(f.Ext))
			if size > 0 && formatSize(*audio) > 0 {
				size += formatSize(*audio)
			} else {
				size = 0
			}
		} else {
			description += fmt.Sprintf(" + %s audio in one file.", readableCodec(f.ACodec))
		}
		if size > 0 {
			description += " Estimated download: " + readableDownloadSize(size) + "."
		} else {
			description += " Size unavailable."
		}
		label := resolutionLabel(min(f.Width, f.Height))
		if f.FPS > 0 {
			label += fmt.Sprintf(" · %g fps", f.FPS)
		}
		if f.DynamicRange != "" {
			label += " · " + f.DynamicRange
		}
		if container != "" {
			label += " · " + container
		}
		label += " · " + readableCodec(f.VCodec)
		options = append(options, linkDownloadQualityData{Label: label, Quality: quality, Description: description})
	}
	if audio != nil {
		options = append(options, linkDownloadQualityData{Label: "Audio only · " + strings.ToUpper(audio.Ext), Quality: audio.FormatID, Description: "Original " + readableCodec(audio.ACodec) + " audio without video."})
	}
	return options
}

func formatSize(f ytDLPFormat) float64 {
	if f.Filesize > 0 {
		return f.Filesize
	}
	return f.FilesizeApprox
}
func readableDownloadSize(size float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for size >= 1024 && i < len(units)-1 {
		size /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", size, units[i])
}
func resolutionLabel(height int) string {
	switch height {
	case 4320:
		return "8K (4320p)"
	case 2160:
		return "4K (2160p)"
	case 1440:
		return "1440p QHD"
	case 1080:
		return "1080p Full HD"
	case 720:
		return "720p HD"
	default:
		return fmt.Sprintf("%dp", height)
	}
}

func readableCodec(codec string) string {
	c := strings.ToLower(codec)
	withDepth := func(name string) string {
		parts := strings.Split(c, ".")
		if len(parts) >= 4 {
			switch parts[3] {
			case "08":
				return name + " (8-bit)"
			case "10":
				return name + " (10-bit)"
			case "12":
				return name + " (12-bit)"
			}
		}
		return name
	}
	switch {
	case strings.HasPrefix(c, "avc"), strings.HasPrefix(c, "h264"):
		return "H.264"
	case strings.HasPrefix(c, "hev"), strings.HasPrefix(c, "hvc"), strings.HasPrefix(c, "hevc"):
		return "HEVC (H.265)"
	case strings.HasPrefix(c, "av01"), c == "av1":
		return withDepth("AV1")
	case strings.HasPrefix(c, "vp09"), strings.HasPrefix(c, "vp9"):
		return withDepth("VP9")
	case strings.HasPrefix(c, "vp08"), strings.HasPrefix(c, "vp8"):
		return "VP8"
	case strings.HasPrefix(c, "mp4a"), c == "aac":
		return "AAC"
	case c == "opus":
		return "Opus"
	case c == "vorbis":
		return "Vorbis"
	default:
		return strings.ToUpper(codec)
	}
}
