import { describe, expect, it } from "vitest";
import {
  useDownloadQualities,
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
  const presets = [{ label: "4K", quality: defaultVideoQuality }];
  const original = {
    label: "Original file",
    quality: "best",
    description: "Original quality cannot be changed.",
  };

  it("preserves strict video presets when auto discovery falls back to direct", () => {
    expect(
      resolveDownloadQualityOptions(
        "auto",
        { ...response, downloader: "direct" },
        presets,
        original
      )
    ).toEqual(presets);
  });

  it("keeps only original file before or after failed explicit direct discovery", () => {
    expect(
      resolveDownloadQualityOptions("direct", null, presets, original)
    ).toEqual([original]);
  });

  it("retains strict 4K alongside discovered video formats", () => {
    expect(
      resolveDownloadQualityOptions("yt-dlp", response, presets, original)
    ).toEqual([...presets, option]);
  });
});
