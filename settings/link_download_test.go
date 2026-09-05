package settings

import "testing"

func TestLinkDownloadMigratesLegacyBestToStrict4K(t *testing.T) {
	for _, old := range []string{"", "best", "bestvideo*+bestaudio/best"} {
		cfg := LinkDownload{DefaultQuality: old}
		cfg.ApplyDefaults()
		want := "bv*[height>=2160][width>=2160]+ba/b[height>=2160][width>=2160]"
		if cfg.DefaultQuality != want {
			t.Errorf("legacy %q became %q, want strict4K", old, cfg.DefaultQuality)
		}
	}
	cfg := LinkDownload{DefaultQuality: "custom-format"}
	cfg.ApplyDefaults()
	if cfg.DefaultQuality != "custom-format" {
		t.Fatal("custom default must be preserved")
	}
}
