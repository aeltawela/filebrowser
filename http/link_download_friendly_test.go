package fbhttp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestFriendlyOptionsExposeExactStreamsAndAudioChoices(t *testing.T) {
	formats := []ytDLPFormat{
		{FormatID: "en", VCodec: "none", ACodec: "opus", Language: "en", Filesize: 1000, Ext: "webm"},
		{FormatID: "de", VCodec: "none", ACodec: "mp4a.40.2", Language: "de", Filesize: 2000, Ext: "m4a"},
		{FormatID: "hd", Width: 1920, Height: 1080, VCodec: "avc1", ACodec: "none", FPS: 30, Ext: "mp4", Filesize: 1000000},
		{FormatID: "uhd", Width: 3840, Height: 2160, VCodec: "av01", ACodec: "none", FPS: 60, Ext: "mp4", Filesize: 4000000},
	}
	options := qualityOptionsFromFormats(formats)
	var exact, audio bool
	for _, o := range options {
		if o.Quality == "uhd+de" {
			exact = true
			if o.AudioVariants["en"].Quality != "uhd+en" {
				t.Errorf("missing exact language pair: %+v", o)
			}
			for _, word := range []string{"Picture:", "Sound: Included", "Download size:", "Playback:"} {
				if !strings.Contains(o.Description, word) {
					t.Errorf("missing %s: %s", word, o.Description)
				}
			}
			if strings.Contains(o.Description, "re-encoding") || strings.Contains(o.Description, "acodec") {
				t.Errorf("technical prose leaked: %s", o.Description)
			}
			if !strings.Contains(o.TechnicalDetails, "3840") {
				t.Error("technical detail lost")
			}
		}
		if o.AudioOnly {
			audio = true
			if o.AudioVariants["en"].Quality != "en" {
				t.Error("audio language unavailable")
			}
		}
	}
	if !exact || !audio {
		t.Fatalf("missing stream/audio: %v %v", exact, audio)
	}
}

func TestProbeDiscoversSafeLanguageChoices(t *testing.T) {
	binary := writeYTDLPTestScript(t, `echo '{"formats":[{"format_id":"a","vcodec":"none","acodec":"opus","language":"de"},{"format_id":"v","width":3840,"height":2160,"vcodec":"av01","acodec":"none"}],"subtitles":{"en":[{"url":"https://example.com/sub.vtt"}],"en.*":[{"url":"https://example.com/sub.vtt"}]},"automatic_captions":{"en":[{"url":"https://example.com/sub.vtt"}],"de":[{"url":"https://example.com/sub.vtt"}],"live_chat":[{"url":"https://example.com/sub.vtt"}]}}'`)
	result, err := ytDLPQualityOptions(context.Background(), binary, "https://example.com/video")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.AudioLanguages) != 1 || result.AudioLanguages[0] != "de" {
		t.Errorf("bad audio languages: %+v", result.AudioLanguages)
	}
	if len(result.SubtitleLanguages) != 2 {
		t.Fatalf("bad subtitle options: %+v", result.SubtitleLanguages)
	}
	for _, s := range result.SubtitleLanguages {
		if (s.Language == "en" && s.Automatic) || (s.Language == "de" && !s.Automatic) {
			t.Errorf("bad subtitle source: %+v", s)
		}
	}
}

func TestSourceDefaultAimsFor4K(t *testing.T) {
	formats := []ytDLPFormat{
		{FormatID: "8k", Width: 7680, Height: 4320, VCodec: "vp9", ACodec: "aac"},
		{FormatID: "4khdr", Width: 3840, Height: 2160, VCodec: "av01", ACodec: "aac", DynamicRange: "HDR10", FPS: 60},
		{FormatID: "4ksdr", Width: 3840, Height: 2160, VCodec: "vp9", ACodec: "aac", DynamicRange: "SDR", FPS: 60},
		{FormatID: "hd", Width: 1920, Height: 1080, VCodec: "avc1", ACodec: "aac"},
	}
	options := qualityOptionsFromFormats(formats)
	if len(options) != 4 || options[0].Quality != "4ksdr" {
		t.Fatalf("expected exact balanced 4K first: %+v", options)
	}
}

func TestSubtitleChoicesExcludeEmptyTracks(t *testing.T) {
	var metadata ytDLPMetadata
	if err := json.Unmarshal([]byte(`{"subtitles":{"en":null,"de":[],"fr":[{}],"es":[{"url":"https://example.com/es.vtt"}]},"automatic_captions":{"it":[null],"pt":{},"ja":[{"url":""}]}}`), &metadata); err != nil {
		t.Fatal(err)
	}
	result := subtitleLanguageChoices(metadata)
	if len(result) != 1 || result[0].Language != "es" {
		t.Fatalf("unavailable subtitle languages exposed: %+v", result)
	}
}

func TestSubtitleChoicesExcludeTranslatedAutomaticCaptions(t *testing.T) {
	var metadata ytDLPMetadata
	if err := json.Unmarshal([]byte(`{"subtitles":{"es":[{"url":"https://example.com/caption?tlang=es"}]},"automatic_captions":{"en-orig":[{"url":"https://example.com/caption?lang=en"}],"en":[{"url":"https://example.com/caption?lang=en"}],"de":[{"url":"https://example.com/caption?lang=en&tlang=de"}],"fr":[{"url":"https://example.com/caption?lang=en&tlang=fr"}]}}`), &metadata); err != nil {
		t.Fatal(err)
	}
	result := subtitleLanguageChoices(metadata)
	if len(result) != 2 || result[0].Language != "en" || result[1].Language != "es" {
		t.Fatalf("synthetic translation choices exposed: %+v", result)
	}
}

func TestSourceOptionsMarkOneMainPerResolution(t *testing.T) {
	formats := []ytDLPFormat{
		{FormatID: "4k30", Width: 3840, Height: 2160, VCodec: "vp9", ACodec: "aac", FPS: 30},
		{FormatID: "4k60", Width: 3840, Height: 2160, VCodec: "vp9", ACodec: "aac", FPS: 60},
		{FormatID: "hd", Width: 1920, Height: 1080, VCodec: "avc1", ACodec: "aac"},
	}
	options := qualityOptionsFromFormats(formats)
	if len(options) != 3 || options[0].Quality != "4k60" || !options[0].Recommended || options[0].Advanced || options[1].Advanced || !options[2].Advanced {
		t.Fatalf("incorrect main and advanced choices: %+v", options)
	}
	for _, option := range options {
		if option.AudioOnly {
			t.Fatal("no separate audio track exists")
		}
	}
}

func TestSourceRecommendationUsesAvailableResolution(t *testing.T) {
	for _, tt := range []struct {
		name    string
		heights []int
		want    int
	}{
		{"below4k", []int{360, 1080}, 1080}, {"above4k", []int{4320, 2880}, 2880},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var formats []ytDLPFormat
			for _, h := range tt.heights {
				formats = append(formats, ytDLPFormat{FormatID: fmt.Sprint(h), Width: h * 2, Height: h, VCodec: "vp9", ACodec: "aac"})
			}
			options := qualityOptionsFromFormats(formats)
			if len(options) != len(formats) || options[0].Quality != fmt.Sprint(tt.want) || !options[0].Recommended {
				t.Fatalf("unexpected recommendation: %+v", options)
			}
		})
	}
}

func TestSourceVideoOptionsExposeResolution(t *testing.T) {
	options := qualityOptionsFromFormats([]ytDLPFormat{
		{FormatID: "portrait", Width: 1080, Height: 1920, VCodec: "avc1", ACodec: "aac"},
		{FormatID: "landscape", Width: 3840, Height: 2160, VCodec: "vp9", ACodec: "aac"},
		{FormatID: "sound", VCodec: "none", ACodec: "aac"},
	})
	for _, option := range options {
		encoded, err := json.Marshal(option)
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		if err = json.Unmarshal(encoded, &data); err != nil {
			t.Fatal(err)
		}
		want := float64(1080)
		if option.Quality == "landscape" {
			want = 2160
		}
		if option.AudioOnly {
			if _, ok := data["resolution"]; ok {
				t.Fatal("audio option must not have a video resolution")
			}
			continue
		}
		if data["resolution"] != want {
			t.Fatalf("resolution = %v, want %v", data["resolution"], want)
		}
	}
}
