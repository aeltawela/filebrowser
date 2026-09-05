import { describe, expect, it } from "vitest";
import {
  useDownloadQualities,
  recommendedQualityOption,
  conciseQualityLabel,
  downloadLanguageName,
  selectVisibleQuality,
  filterAudioQualityOptions,
  resolveAudioQuality,
  videoDownloadExtras,
  resolveDownloadQualityOptions,
  defaultVideoQuality,
} from "../download-qualities";

const option = { label: "1080p", quality: "137+140" };
const response: LinkDownloadQualities = {
  downloader: "yt-dlp",
  options: [option],
  verified: true,
  notice: "No 4K",
};

describe("download quality discovery", () => {
  it("keeps the automatic default strictly at 4K or higher", () => {
    expect(defaultVideoQuality).toBe(
      "bv*[height>=2160][width>=2160]+ba/b[height>=2160][width>=2160]"
    );
  });

  it("ignores a completed request immediately after the URL changes", async () => {
    const state = useDownloadQualities();
    let resolve!: (value: typeof response) => void;
    const request = state.load(
      () =>
        new Promise((done) => {
          resolve = done;
        })
    );
    state.invalidate(true);
    resolve(response);
    await request;
    expect(state.result.value).toBeNull();
    expect(state.loading.value).toBe(true);
  });

  it("ignores an old failure after a newer result succeeds", async () => {
    const state = useDownloadQualities();
    let reject!: (reason: Error) => void;
    const old = state.load(
      () =>
        new Promise((_, fail) => {
          reject = fail;
        })
    );
    state.invalidate(true);
    await state.load(async () => response);
    reject(new Error("old failure"));
    await old;
    expect(state.result.value).toEqual(response);
    expect(state.error.value).toBe("");
    expect(state.loading.value).toBe(false);
  });

  it("does not restore options when the dialog closes", async () => {
    const state = useDownloadQualities();
    let resolve!: (value: typeof response) => void;
    const request = state.load(
      () =>
        new Promise((done) => {
          resolve = done;
        })
    );
    state.invalidate();
    resolve(response);
    await request;
    expect(state.result.value).toBeNull();
    expect(state.loading.value).toBe(false);
  });

  it("reports current discovery failure for explicit fallback selection", async () => {
    const state = useDownloadQualities();
    await state.load(async () => {
      throw new Error("Discovery unavailable");
    });
    expect(state.error.value).toBe("Discovery unavailable");
    expect(state.loading.value).toBe(false);
  });
});

describe("direct download quality choices", () => {
  const original = {
    label: "Original file",
    quality: "best",
    description: "Original quality cannot be changed.",
  };

  it("does not invent video options when auto discovery falls back to direct", () => {
    expect(
      resolveDownloadQualityOptions(
        "auto",
        { ...response, downloader: "direct" },
        original
      )
    ).toEqual([]);
  });

  it("keeps only original file before or after failed explicit direct discovery", () => {
    expect(resolveDownloadQualityOptions("direct", null, original)).toEqual([
      original,
    ]);
  });

  it("shows only verified source formats without synthetic presets", () => {
    expect(resolveDownloadQualityOptions("yt-dlp", response, original)).toEqual(
      [option]
    );
  });
});

describe("download language and file choices", () => {
  const translated = {
    quality: "video+french",
    description: "Sound: French",
    technicalDetails: "fr",
  };
  const choices: LinkDownloadQualityOption[] = [
    {
      label: "4K",
      quality: defaultVideoQuality,
      audioVariants: { fr: translated },
    },
    { label: "1080p", quality: "video+original" },
  ];

  it("shows only qualities that support the chosen audio language", () => {
    expect(filterAudioQualityOptions(choices, "fr")).toEqual([choices[0]]);
    expect(filterAudioQualityOptions(choices, "")).toEqual(choices);
  });

  it("uses the exact language variant for both download and explanation", () => {
    expect(resolveAudioQuality(choices[0], "fr")).toEqual(translated);
    expect(resolveAudioQuality(choices[1], "fr")).toBeUndefined();
    expect(resolveAudioQuality(choices[1], "")).toEqual(choices[1]);
  });

  it("omits video extras from direct requests and empty subtitle choices", () => {
    expect(videoDownloadExtras("direct", "mp4", "fr")).toEqual({});
    expect(videoDownloadExtras("yt-dlp", "mp4", "fr", true)).toEqual({});
    expect(videoDownloadExtras("auto", "mkv", "")).toEqual({
      container: "mkv",
    });
    expect(videoDownloadExtras("yt-dlp", "mp4", "fr")).toEqual({
      container: "mp4",
      subtitleLanguage: "fr",
    });
  });
});

describe("source option presentation", () => {
  it("keeps the list empty before verified discovery", () => {
    expect(
      resolveDownloadQualityOptions("auto", null, {
        label: "Original",
        quality: "best",
      })
    ).toEqual([]);
    expect(
      resolveDownloadQualityOptions(
        "auto",
        { ...response, verified: false },
        { label: "Original", quality: "best" }
      )
    ).toEqual([]);
  });
  it("hides codec alternatives until more options are requested", () => {
    const advanced = {
      label: "1080p AV1",
      quality: "av1+audio",
      advanced: true,
    };
    expect(filterAudioQualityOptions([option, advanced], "")).toEqual([option]);
    expect(filterAudioQualityOptions([option, advanced], "", true)).toEqual([
      option,
      advanced,
    ]);
  });
  it("puts the source recommended option first", () => {
    const recommended = {
      ...option,
      quality: "recommended",
      recommended: true,
    };
    expect(
      resolveDownloadQualityOptions(
        "auto",
        { ...response, options: [option, recommended] },
        option
      )[0]
    ).toEqual(recommended);
  });
});

describe("selection after filtering", () => {
  const main = { label: "4K", quality: "main", recommended: true };
  it("selects the recommended main choice when an advanced choice is hidden", () => {
    expect(selectVisibleQuality("advanced", [option, main], false)).toBe(
      "main"
    );
  });
  it("selects an available language choice rather than retaining an unavailable selection", () => {
    expect(selectVisibleQuality("original-language", [option], true)).toBe(
      option.quality
    );
  });
  it("preserves a visible choice and manual mode while advanced options are shown", () => {
    expect(selectVisibleQuality(option.quality, [option, main], false)).toBe(
      option.quality
    );
    expect(selectVisibleQuality("custom", [option], true)).toBe("custom");
    expect(selectVisibleQuality("custom", [option], false)).toBe(
      option.quality
    );
  });
});

it("promotes the first language-supported format per resolution", () => {
  const english = {
    label: "1080p",
    quality: "english",
    resolution: 1080,
    audioVariants: { en: { quality: "english", description: "English" } },
  };
  const german = {
    label: "1080p",
    quality: "german",
    resolution: 1080,
    advanced: true,
    audioVariants: { de: { quality: "german", description: "German" } },
  };
  const alternative = { ...german, quality: "german-other" };
  expect(
    filterAudioQualityOptions([english, german, alternative], "de")
  ).toEqual([german]);
  expect(
    filterAudioQualityOptions([english, german, alternative], "de", true)
  ).toEqual([german, alternative]);
});

it("names original subtitle language selectors without changing the selector", () => {
  expect(downloadLanguageName("en-orig")).toBe("English");
});

it("shows simple picture-quality names without codec clutter", () => {
  expect(
    conciseQualityLabel({
      label: "2160p60 AV1 HDR MKV",
      quality: "id",
      resolution: 2160,
    })
  ).toBe("4K Ultra HD");
  expect(
    conciseQualityLabel({ label: "1080p VP9", quality: "id", resolution: 1080 })
  ).toBe("1080p Full HD");
});

it("recommends source 4K when present and otherwise the highest resolution", () => {
  const high = { label: "8K", quality: "8k", resolution: 4320 };
  const four = { label: "4K", quality: "4k", resolution: 2160 };
  const lower = { label: "1440p", quality: "1440", resolution: 1440 };
  expect(recommendedQualityOption([lower, high])).toEqual(high);
  expect(recommendedQualityOption([high, four, lower])).toEqual(four);
});
