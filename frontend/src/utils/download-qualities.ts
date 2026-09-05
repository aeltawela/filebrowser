import { ref } from "vue";

export const defaultVideoQuality =
  "bv*[height>=2160][width>=2160]+ba/b[height>=2160][width>=2160]";

export const qualityMessages = {
  en: {
    quality4K: () => "4K or better · video + audio",
    qualityHighest: () => "Highest available",
    qualityOriginal: () => "Original file",
    qualityTechnical: () => "Technical details",
    fileType: () => "File type",
    mkvRecommended: () => "MKV (Recommended)",
    moreOptions: () => "Show advanced formats",
    fewerOptions: () => "Hide advanced formats",
    advancedFormat: () => "Advanced format",
    advancedHelp: () =>
      "Choose a specific version, or keep the selected quality above.",
    emptyQuality: () => "No downloadable qualities found",
    customQuality: () => "Custom format selected",
    recommended: () => "Recommended",
    recommendationHelp: () =>
      "Balances picture quality and playback support. Keeps the original picture size without enlarging it.",
    chooseQuality: () => "Checking available qualities…",
    waitingQuality: () => "Paste a link to see available qualities",
    mkvHelp: () =>
      "Recommended: keeps the original picture and sound without conversion. Works with most media players.",
    mp4Help: () =>
      "MP4 is widely supported. Picture and sound are not re-encoded, so playback still depends on the selected format.",
    audioLanguage: () => "Audio language",
    audioDefault: () => "Original/default",
    subtitles: () => "Subtitles",
    subtitlesOff: () => "Off",
    subtitlesAutomatic: () => "auto-generated",
    manualLanguageHelp: () => "The custom format controls the audio language.",
    languageUnavailable: () =>
      "Choose a quality that supports this audio language.",
    qualityUnavailable: () =>
      "Could not check video quality. Your choice is unchanged. For the original file, choose Direct.",
    qualityOriginalHelp: () =>
      "Downloads the original file without changing its picture or sound.",
    quality4KHelp: () =>
      "Downloads 4K or higher with sound. If no 4K version is available, choose a lower quality.",
    qualityHighestHelp: () =>
      "Downloads the best picture available with sound. It may be below 4K.",
    qualityVerified: () => "Available choices checked.",
    qualityFallback: () =>
      "No verified choices yet. Paste a link and wait for the available versions.",
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
  original: LinkDownloadQualityOption
): LinkDownloadQualityOption[] {
  if (downloader === "direct") return [original];
  if (!response?.verified || response.downloader === "direct") return [];
  return [...response.options].sort(
    (a, b) => Number(!!b.recommended) - Number(!!a.recommended)
  );
}

export function filterAudioQualityOptions(
  options: LinkDownloadQualityOption[],
  language: string,
  showAdvanced = false
) {
  const supported = language
    ? options.filter((option) => option.audioVariants?.[language])
    : options;
  if (showAdvanced) return supported;
  const primaryResolutions = new Set(
    supported
      .filter((option) => !option.advanced)
      .map((option) => option.resolution)
  );
  return supported.filter((option) => {
    if (!option.advanced) return true;
    if (
      !language ||
      !option.resolution ||
      primaryResolutions.has(option.resolution)
    )
      return false;
    primaryResolutions.add(option.resolution);
    return true;
  });
}

export function resolveAudioQuality(
  option: LinkDownloadQualityOption | undefined,
  language: string
) {
  return language ? option?.audioVariants?.[language] : option;
}
export function videoDownloadExtras(
  downloader: LinkDownloadDownloader,
  container: "mkv" | "mp4",
  subtitleLanguage: string,
  audioOnly = false
) {
  if (downloader === "direct" || audioOnly) return {};
  return { container, ...(subtitleLanguage ? { subtitleLanguage } : {}) };
}

export function downloadLanguageName(language: string) {
  try {
    return (
      new Intl.DisplayNames(["en"], { type: "language" }).of(
        language.replace(/-orig$/, "")
      ) || language
    );
  } catch {
    return language;
  }
}

export function selectVisibleQuality(
  current: string,
  options: LinkDownloadQualityOption[],
  showAdvanced: boolean
) {
  if (
    (current === "custom" && showAdvanced) ||
    options.some((option) => option.quality === current)
  )
    return current;
  return recommendedQualityOption(options)?.quality || "";
}

export function conciseQualityLabel(option: LinkDownloadQualityOption) {
  if (!option.resolution) return option.label;
  const names: Record<number, string> = {
    4320: "8K Ultra HD",
    2160: "4K Ultra HD",
    1440: "1440p QHD",
    1080: "1080p Full HD",
    720: "720p HD",
  };
  return names[option.resolution] || `${option.resolution}p`;
}

export function recommendedQualityOption(options: LinkDownloadQualityOption[]) {
  const videos = options.filter((option) => !option.audioOnly);
  const target = videos.some((option) => option.resolution === 2160)
    ? 2160
    : Math.max(0, ...videos.map((option) => option.resolution || 0));
  const candidates = videos.filter(
    (option) => (option.resolution || 0) === target
  );
  return (
    candidates.find((option) => option.recommended) ||
    candidates[0] ||
    options[0]
  );
}
