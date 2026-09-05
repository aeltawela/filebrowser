import { describe, expect, it } from "vitest";
import { createSSRApp, h } from "vue";
import { renderToString } from "vue/server-renderer";
import QualityDescription from "../QualityDescription.vue";

const render = (description?: string, technicalDetails?: string) =>
  renderToString(
    createSSRApp({
      render: () =>
        h(QualityDescription, {
          description,
          technicalDetails,
          technicalLabel: "Technical details",
        }),
    })
  );

describe("quality explanation", () => {
  it("shows the readable description and keeps technical details collapsed", async () => {
    const html = await render(
      "Picture: 4K\nSound: Included",
      "VP9 · 3840×2160"
    );
    expect(html).toContain("Picture: 4K\nSound: Included");
    expect(html).toMatch(/<summary[^>]*>Technical details<\/summary>/);
    expect(html).toContain("VP9 · 3840×2160");
    expect(html).not.toMatch(/<details[^>]*\bopen\b/);
  });

  it("omits technical controls when no technical details are available", async () => {
    expect(await render("Downloads 4K or higher with sound.")).not.toContain(
      "<details"
    );
  });

  it("renders source descriptions as text rather than HTML", async () => {
    const html = await render(
      "<img src=x onerror=alert(1)>",
      "<script>alert(1)</script>"
    );
    expect(html).not.toContain("<img");
    expect(html).not.toContain("<script>");
    expect(html).toContain("&lt;img");
  });
});
