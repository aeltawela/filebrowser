import { ref } from "vue";

export const defaultVideoQuality =
  "bv*[height>=2160][width>=2160]+ba/b[height>=2160][width>=2160]";

export const qualityMessages = {
  en: {
    quality4K: () => "4K or better · video + audio",
    qualityHighest: () => "Highest available",
    qualityOriginal: () => "Original file",
    qualityUnavailable: () =>
      "Video format discovery is unavailable. The selected video quality is preserved. To download an unchanged file, explicitly choose the Direct downloader.",
    qualityOriginalHelp: () =>
      "Downloads the original file. Its resolution, audio and quality cannot be changed.",
    quality4KHelp: () =>
      "Requires 4K or higher video with audio. If unavailable, select a lower quality explicitly; this preset never falls back below 4K.",
    qualityHighestHelp: () =>
      "Downloads the highest available quality, which may be below 4K.",
    qualityVerified: () =>
      "Source formats verified. Choose a specific resolution, frame rate and container below.",
    qualityFallback: () =>
      "Source formats are unverified. These presets will be checked when the download starts.",
  },
};

export function useDownloadQualities() {
  const result = ref<LinkDownloadQualities | null>(null);
  const loading = ref(false);
  const error = ref("");
  let serial = 0;

  const invalidate = (pending = false) => {
    serial++;
    result.value = null;
    error.value = "";
    loading.value = pending;
  };

  const load = async (fetch: () => Promise<LinkDownloadQualities>) => {
    const request = ++serial;
    loading.value = true;
    try {
      const response = await fetch();
      if (request !== serial) return;
      result.value = response;
      error.value = response.error || "";
    } catch (cause) {
      if (request !== serial) return;
      error.value = cause instanceof Error ? cause.message : String(cause);
    } finally {
      if (request === serial) loading.value = false;
    }
  };

  return { result, loading, error, invalidate, load };
}

export function resolveDownloadQualityOptions(
  downloader: LinkDownloadDownloader,
  response: LinkDownloadQualities | null,
  presets: LinkDownloadQualityOption[],
  original: LinkDownloadQualityOption
): LinkDownloadQualityOption[] {
  if (downloader === "direct") return [original];
  if (response?.downloader === "direct") return presets;
  const options = response?.options || [];
  return [
    ...presets.map(
      (preset) =>
        options.find((option) => option.quality === preset.quality) || preset
    ),
    ...options.filter(
      (option) => !presets.some((preset) => preset.quality === option.quality)
    ),
  ];
}
