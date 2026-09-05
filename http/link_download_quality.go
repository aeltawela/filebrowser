package fbhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/filebrowser/filebrowser/v2/settings"
)

const highestAvailableQuality = "bv*+ba/b"

var ytDLPLanguagePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,34}$`)

var ytDLPFormatIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)

func defaultLinkDownloadQualities(downloader string) []linkDownloadQualityData {
	if downloader == linkDownloaderDirect {
		return []linkDownloadQualityData{{Label: "Original file", Quality: "best", Description: "Keeps the file exactly as the website provides it."}}
	}
	return []linkDownloadQualityData{
		{Label: "4K or better · video + audio", Quality: settings.DefaultLinkDownloadQuality, Description: "Downloads 4K or higher picture quality with sound.\nIf this website has no 4K version, choose a lower quality. This option will not downgrade automatically."},
		{Label: "Highest available · any resolution", Quality: highestAvailableQuality, Description: "Downloads the clearest version this website offers, with sound. It may be below 4K."},
		{Label: "Audio only", Quality: "bestaudio/best", Description: "Saves the sound without the video."},
	}
}

type ytDLPMetadata struct {
	ytDLPFormat
	Formats           []ytDLPFormat              `json:"formats"`
	Subtitles         map[string]json.RawMessage `json:"subtitles"`
	AutomaticCaptions map[string]json.RawMessage `json:"automatic_captions"`
}
type ytDLPFormat struct {
	URL            string  `json:"url"`
	FormatID       string  `json:"format_id"`
	Language       string  `json:"language"`
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
	if len(metadata.Formats) == 0 && usableOriginalVideo(metadata.ytDLPFormat) {
		metadata.Formats = []ytDLPFormat{metadata.ytDLPFormat}
	}
	result := linkDownloadQualitiesData{Options: qualityOptionsFromFormats(metadata.Formats), Verified: true, AudioLanguages: audioLanguageChoices(metadata.Formats), SubtitleLanguages: subtitleLanguageChoices(metadata)}
	if warnings.Len() > 0 {
		result.Notice = strings.TrimSpace(result.Notice + " This website may not have shared every option. Try the link again if a quality is missing.")
	}
	if len(result.Options) == 0 {
		result.Verified = false
		result.Notice = "No downloadable formats were reported by this website."
	}
	return result, nil
}

func usableFormat(f ytDLPFormat) bool {
	return !f.HasDRM && ytDLPFormatIDPattern.MatchString(f.FormatID)
}
func usableVideoFormat(f ytDLPFormat) bool {
	return usableFormat(f) && f.Width > 0 && f.Height > 0 && f.VCodec != "none" && (f.VCodec != "" || usableOriginalVideo(f))
}

func qualityOptionsFromFormats(formats []ytDLPFormat) []linkDownloadQualityData {
	options := make([]linkDownloadQualityData, 0)
	var audio *ytDLPFormat
	audioByLanguage := map[string]*ytDLPFormat{}
	for i := range formats {
		f := &formats[i]
		if usableFormat(*f) && f.VCodec == "none" && f.ACodec != "" && f.ACodec != "none" {
			audio = f
			if ytDLPLanguagePattern.MatchString(f.Language) {
				audioByLanguage[f.Language] = f
			}
		}
	}
	grouped := map[string]ytDLPFormat{}
	for _, f := range formats {
		if !usableVideoFormat(f) {
			continue
		}
		container := f.Ext
		if f.ACodec == "none" {
			container = "merged"
		}
		key := fmt.Sprintf("%dx%d/%g/%s/%s/%s/%s", f.Width, f.Height, f.FPS, f.DynamicRange, container, strings.ToLower(f.VCodec), f.Language)
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
		aSDR, bSDR := isSDR(a), isSDR(b)
		if aSDR != bSDR {
			return aSDR
		}
		if a.FPS != b.FPS {
			return a.FPS > b.FPS
		}
		if codecPreference(a) != codecPreference(b) {
			return codecPreference(a) < codecPreference(b)
		}
		return a.FormatID < b.FormatID
	})
	seenResolutions := map[int]bool{}
	recommendedResolution := 0
	for _, f := range videos {
		r := min(f.Width, f.Height)
		if r == 2160 {
			recommendedResolution = r
		}
	}
	if recommendedResolution == 0 && len(videos) > 0 {
		recommendedResolution = min(videos[0].Width, videos[0].Height)
	}
	for _, f := range videos {
		option := videoQualityOption(f, audio)
		r := min(f.Width, f.Height)
		option.Advanced = seenResolutions[r]
		option.Recommended = !option.Advanced && r == recommendedResolution
		seenResolutions[r] = true
		option.AudioVariants = map[string]linkDownloadAudioVariant{}
		if f.ACodec == "none" {
			for language, track := range audioByLanguage {
				variant := videoQualityOption(f, track)
				option.AudioVariants[language] = linkDownloadAudioVariant{Quality: variant.Quality, Description: variant.Description, TechnicalDetails: variant.TechnicalDetails}
			}
		} else if ytDLPLanguagePattern.MatchString(f.Language) {
			option.AudioVariants[f.Language] = linkDownloadAudioVariant{Quality: option.Quality, Description: option.Description, TechnicalDetails: option.TechnicalDetails}
		}
		options = append(options, option)
	}
	for _, f := range formats {
		if usableVideoFormat(f) || !usableOriginalVideo(f) {
			continue
		}
		options = append(options, linkDownloadQualityData{
			Label: "Original video · quality not reported", Quality: f.FormatID,
			Description:      "Picture: Quality not reported by this website.\nSound: " + sourceSoundDescription(f.ACodec) + "\nDownload size: " + friendlyDownloadSize(formatSize(f)),
			TechnicalDetails: "Original " + strings.ToUpper(f.Ext) + " source file. No resizing or re-encoding.",
		})
	}
	if audio != nil {
		option := audioQualityOption(*audio)
		option.AudioVariants = map[string]linkDownloadAudioVariant{}
		for language, track := range audioByLanguage {
			variant := audioQualityOption(*track)
			option.AudioVariants[language] = linkDownloadAudioVariant{Quality: variant.Quality, Description: variant.Description, TechnicalDetails: variant.TechnicalDetails}
		}
		options = append(options, option)
	}
	sort.SliceStable(options, func(i, j int) bool {
		if options[i].Recommended != options[j].Recommended {
			return options[i].Recommended
		}
		return !options[i].Advanced && options[j].Advanced
	})
	if len(videos) == 0 && len(options) > 0 {
		options[0].Recommended = true
	}
	return options
}

func usableOriginalVideo(f ytDLPFormat) bool {
	if !usableFormat(f) || f.VCodec == "none" {
		return false
	}
	parsed, err := url.Parse(f.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	switch strings.ToLower(f.Ext) {
	case "mp4", "webm", "mkv", "mov", "m4v", "avi", "ts", "mpeg", "mpg", "ogv", "flv":
		return true
	default:
		return false
	}
}

func sourceSoundDescription(codec string) string {
	if codec == "" {
		return "Not reported by this website."
	}
	if codec == "none" {
		return "No audio reported."
	}
	return "Included."
}

func isSDR(f ytDLPFormat) bool {
	return f.DynamicRange == "" || strings.EqualFold(f.DynamicRange, "SDR")
}

func codecPreference(f ytDLPFormat) int {
	codec := readableCodec(f.VCodec)
	if min(f.Width, f.Height) <= 1080 && strings.HasPrefix(codec, "H.264") {
		return 0
	}
	switch {
	case strings.HasPrefix(codec, "VP9"):
		return 1
	case strings.HasPrefix(codec, "HEVC"):
		return 2
	case strings.HasPrefix(codec, "AV1"):
		return 3
	case strings.HasPrefix(codec, "H.264"):
		return 4
	default:
		return 5
	}
}

func usableSubtitleTracks(raw json.RawMessage, automatic bool) bool {
	var tracks []struct {
		URL  string `json:"url"`
		Data string `json:"data"`
	}
	if json.Unmarshal(raw, &tracks) != nil {
		return false
	}
	for _, track := range tracks {
		if strings.TrimSpace(track.Data) != "" {
			return true
		}
		parsed, err := url.Parse(track.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			continue
		}
		if automatic && parsed.Query().Get("tlang") != "" {
			continue
		}
		return true
	}
	return false
}

func videoQualityOption(f ytDLPFormat, audio *ytDLPFormat) linkDownloadQualityData {
	quality, size, acodec := f.FormatID, formatSize(f), f.ACodec
	if f.ACodec == "none" && audio != nil {
		quality += "+" + audio.FormatID
		acodec = audio.ACodec
		if size > 0 && formatSize(*audio) > 0 {
			size += formatSize(*audio)
		} else {
			size = 0
		}
	}
	label := resolutionLabel(min(f.Width, f.Height))
	picture := "Picture: " + label
	if f.FPS > 0 {
		label += fmt.Sprintf(" · %g fps", f.FPS)
		if f.FPS >= 50 {
			picture += fmt.Sprintf(", smooth motion (%g fps)", f.FPS)
		} else {
			picture += fmt.Sprintf(" (%g fps)", f.FPS)
		}
	}
	if f.DynamicRange != "" && f.DynamicRange != "SDR" {
		label += " · " + f.DynamicRange
		picture += " with HDR colour"
	}
	if f.VCodec != "" {
		label += " · " + readableCodec(f.VCodec)
	}
	playback := "Works in most media players."
	if !strings.HasPrefix(readableCodec(f.VCodec), "H.264") {
		playback = "May need a newer device or media player."
	}
	description := picture + ".\nSound: " + sourceSoundDescription(acodec) + "\nDownload size: " + friendlyDownloadSize(size) + "\nPlayback: " + playback
	technical := fmt.Sprintf("%d × %d; %s video + %s audio. Source video: %s. Original quality is preserved without re-encoding.", f.Width, f.Height, readableCodec(f.VCodec), readableCodec(acodec), strings.ToUpper(f.Ext))
	return linkDownloadQualityData{Resolution: min(f.Width, f.Height), Label: label, Quality: quality, Description: description, TechnicalDetails: technical}
}

func audioQualityOption(f ytDLPFormat) linkDownloadQualityData {
	return linkDownloadQualityData{Label: "Audio only · no video", Quality: f.FormatID, AudioOnly: true, Description: "Picture: None (audio only).\nSound: Included.\nDownload size: " + friendlyDownloadSize(formatSize(f)) + "\nPlayback: Original sound file.", TechnicalDetails: "Original " + readableCodec(f.ACodec) + " audio in a " + strings.ToUpper(f.Ext) + " file."}
}

func friendlyDownloadSize(size float64) string {
	if size <= 0 {
		return "Not provided by this website."
	}
	return "About " + readableDownloadSize(size) + "."
}

func audioLanguageChoices(formats []ytDLPFormat) []string {
	found := map[string]bool{}
	for _, f := range formats {
		if usableFormat(f) && f.ACodec != "" && f.ACodec != "none" && ytDLPLanguagePattern.MatchString(f.Language) {
			found[f.Language] = true
		}
	}
	result := make([]string, 0, len(found))
	for language := range found {
		result = append(result, language)
	}
	sort.Strings(result)
	return result
}

func subtitleLanguageChoices(metadata ytDLPMetadata) []linkDownloadSubtitleLanguage {
	found := map[string]bool{}
	for language, tracks := range metadata.AutomaticCaptions {
		if usableSubtitleTracks(tracks, true) && language != "live_chat" && ytDLPLanguagePattern.MatchString(language) {
			found[language] = true
		}
	}
	for language, tracks := range metadata.Subtitles {
		if usableSubtitleTracks(tracks, false) && language != "live_chat" && ytDLPLanguagePattern.MatchString(language) {
			found[language] = false
		}
	}
	for language, automatic := range found {
		if automatic && strings.HasSuffix(language, "-orig") {
			if _, exists := found[strings.TrimSuffix(language, "-orig")]; exists {
				delete(found, language)
			}
		}
	}
	names := make([]string, 0, len(found))
	for language := range found {
		names = append(names, language)
	}
	sort.Strings(names)
	result := make([]linkDownloadSubtitleLanguage, 0, len(names))
	for _, language := range names {
		result = append(result, linkDownloadSubtitleLanguage{Language: language, Automatic: found[language]})
	}
	return result
}

func formatSize(f ytDLPFormat) float64 {
	if f.Filesize > 0 {
		return f.Filesize
	}
	return f.FilesizeApprox
}
func readableDownloadSize(size float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for size >= 1000 && i < len(units)-1 {
		size /= 1000
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
