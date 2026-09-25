import { test } from "node:test";
import assert from "node:assert/strict";
import { measureWidth, truncateToWidth } from "../src/metrics.ts";
import { collectStatusText } from "../src/nodeTree.ts";
import { fixtureGlyphMetrics } from "../fixtures/glyphMetrics.ts";
import { fixtureStatusTree } from "../fixtures/statusTree.ts";

test("collectStatusText walks the fixture tree and skips hidden nodes", () => {
  assert.equal(collectStatusText(fixtureStatusTree), "上传进度 42%");
});

test("measureWidth sums fixture glyph advances (no layout engine)", () => {
  assert.equal(measureWidth("abc", fixtureGlyphMetrics), 450 + 480 + 430);
  assert.equal(measureWidth("上传", fixtureGlyphMetrics), 2000);
  assert.equal(measureWidth("🙂", fixtureGlyphMetrics), 500); // defaultAdvance fallback
});

test("truncateToWidth is a no-op when the text fits", () => {
  assert.equal(truncateToWidth("abc", fixtureGlyphMetrics, 10_000), "abc");
});

test("truncateToWidth fits text plus ellipsis within maxWidth", () => {
  const out = truncateToWidth("上传进度更新可用", fixtureGlyphMetrics, 3500);
  assert.equal(out, "上传…");
  assert.ok(measureWidth(out, fixtureGlyphMetrics) <= 3500);
});

test("truncateToWidth returns empty when even the ellipsis does not fit", () => {
  assert.equal(truncateToWidth("abc", fixtureGlyphMetrics, 100), "");
});
