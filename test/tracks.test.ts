import { test } from "node:test";
import assert from "node:assert/strict";

import { sizeTracks, resolveFractions } from "../src/tracks.ts";
import { fixtureFont, measureText, minContentWidth } from "../src/glyphs.ts";
import { mixedGrid, crampedGrid, zeroWeightGrid, spanningGrid } from "../src/fixtures.ts";
import type { LayoutContainer } from "../src/tree.ts";

const nearly = (actual: number, expected: number, eps = 1e-9) =>
  assert.ok(Math.abs(actual - expected) <= eps, `${actual} != ${expected}`);

test("glyph metrics: text width derives from fixture advances only", () => {
  // "ab" = (500 + 520) / 1000 * 16
  nearly(measureText("ab", 16, fixtureFont), 16.32);
  // min-content is the widest word
  nearly(minContentWidth("a mm", 16, fixtureFont), (1600 / 1000) * 16);
});

test("fixed tracks resolve to their exact pixel size", () => {
  const container: LayoutContainer = {
    width: 500,
    gap: 10,
    columns: [
      { kind: "fixed", px: 120 },
      { kind: "fixed", px: 80 },
    ],
    items: [],
  };
  const { sizes, diagnostics } = sizeTracks(container, fixtureFont);
  assert.deepEqual(sizes, [120, 80]);
  assert.deepEqual(diagnostics, []);
});

test("content tracks size to the widest unbreakable word", () => {
  const container: LayoutContainer = {
    width: 500,
    gap: 0,
    columns: [{ kind: "content" }],
    items: [{ id: "t", column: 0, columnSpan: 1, text: "alpha beta", fontSize: 16 }],
  };
  const { sizes } = sizeTracks(container, fixtureFont);
  // widest word "alpha": (500+200+500+? ) -> computed from the fixture font
  nearly(sizes[0], minContentWidth("alpha beta", 16, fixtureFont));
});

test("fraction tracks split leftover space proportionally after gaps and fixed tracks", () => {
  const container: LayoutContainer = {
    width: 800,
    gap: 10,
    columns: [
      { kind: "fixed", px: 160 },
      { kind: "content" },
      { kind: "fraction", weight: 2 },
      { kind: "fraction", weight: 1 },
    ],
    items: [{ id: "nav", column: 1, columnSpan: 1, text: "alpha beta", fontSize: 16 }],
  };
  const { sizes, diagnostics } = sizeTracks(container, fixtureFont);
  const contentWidth = minContentWidth("alpha beta", 16, fixtureFont);
  const leftover = 800 - 160 - contentWidth - 3 * 10;
  nearly(sizes[0], 160);
  nearly(sizes[1], contentWidth);
  nearly(sizes[2], (leftover * 2) / 3);
  nearly(sizes[3], leftover / 3);
  assert.deepEqual(diagnostics, []);
});

test("spanning item minimum width constrains the total grid width", () => {
  const { sizes, diagnostics } = sizeTracks(spanningGrid, fixtureFont);
  const wideMin = minContentWidth("ommmmmmmmmmmmmmmmmmmmmmmmm", 16, fixtureFont);
  // The two tracks plus the gap between them must fit the spanning item.
  assert.ok(sizes[0] + sizes[1] + spanningGrid.gap >= wideMin - 1e-9);
  // Single-column items raise each fraction floor first; the spanning item
  // then covers the remaining deficit equally across the spanned tracks.
  const floor0 = minContentWidth("a", 16, fixtureFont);
  const floor1 = minContentWidth("b", 16, fixtureFont);
  const topUp = (wideMin - spanningGrid.gap - floor0 - floor1) / 2;
  const frozenFloor = floor1 + topUp;
  // Unequal weights: the 1fr track freezes at its floor, the 3fr track
  // absorbs the rest of the container.
  nearly(sizes[1], frozenFloor, 1e-9);
  nearly(sizes[0], spanningGrid.width - spanningGrid.gap - frozenFloor, 1e-9);
  assert.deepEqual(diagnostics, []);
});

test("spanning item wider than the container reports overflow, never negative widths", () => {
  const container: LayoutContainer = {
    width: 200,
    gap: 20,
    columns: [
      { kind: "fraction", weight: 1 },
      { kind: "fraction", weight: 1 },
    ],
    items: [
      { id: "wide", column: 0, columnSpan: 2, text: "ommmmmmmmmmmmmmmmmmmmmmmmm", fontSize: 16 },
    ],
  };
  const { sizes, diagnostics } = sizeTracks(container, fixtureFont);
  const wideMin = minContentWidth("ommmmmmmmmmmmmmmmmmmmmmmmm", 16, fixtureFont);
  for (const size of sizes) assert.ok(size >= 0, "no negative track sizes");
  const overflow = diagnostics.find((d) => d.kind === "container");
  assert.ok(overflow, "container overflow reported");
  nearly(overflow!.deficit, wideMin - container.width, 1e-9);
});

test("container smaller than fixed tracks reports overflow and clamps fractions to floors", () => {
  const { sizes, diagnostics } = sizeTracks(crampedGrid, fixtureFont);
  assert.deepEqual(sizes, [80, 60, 0]);
  assert.equal(diagnostics.length, 1);
  assert.equal(diagnostics[0].kind, "container");
  nearly(diagnostics[0].deficit, 80 + 60 + 2 * 10 - 100);
});

test("zero-weight fractions never divide by zero and receive only their floor", () => {
  const { sizes, diagnostics } = sizeTracks(zeroWeightGrid, fixtureFont);
  assert.deepEqual(sizes, [0, 40, 100]);
  for (const size of sizes) assert.ok(Number.isFinite(size));
  // 140 <= 500 so no overflow.
  assert.deepEqual(diagnostics, []);
});

test("item spanning only fixed tracks that do not fit is diagnosed", () => {
  const container: LayoutContainer = {
    width: 500,
    gap: 10,
    columns: [
      { kind: "fixed", px: 30 },
      { kind: "fixed", px: 30 },
    ],
    items: [{ id: "x", column: 0, columnSpan: 2, text: "ommmmmmmmmmmmmmmmmmmmmmmmm", fontSize: 16 }],
  };
  const { diagnostics } = sizeTracks(container, fixtureFont);
  const itemDiag = diagnostics.find((d) => d.kind === "item" && d.itemId === "x");
  assert.ok(itemDiag, "item overflow diagnostic present");
  assert.ok(itemDiag!.deficit > 0);
});

test("fraction distribution converges within track-count + 1 iterations", () => {
  // Many tracks with conflicting floors and weights.
  const weights = [5, 0, 3, 1, 0, 2, 4, 1];
  const floors = [10, 0, 90, 50, 0, 5, 200, 30];
  const { sizes, iterations } = resolveFractions(weights, floors, 1000);
  assert.ok(iterations <= weights.length + 1, `converged in ${iterations} iterations`);
  for (let i = 0; i < weights.length; i++) {
    assert.ok(sizes[i] >= floors[i], `track ${i} respects its floor`);
    assert.ok(Number.isFinite(sizes[i]));
  }
  const total = sizes.reduce((a, b) => a + b, 0);
  nearly(total, 1000, 1e-6);
});

test("all-zero weights with leftover space terminate immediately", () => {
  const { sizes, iterations } = resolveFractions([0, 0], [10, 20], 500);
  assert.deepEqual(sizes, [10, 20]);
  assert.equal(iterations, 1);
});

test("mixed fixture grid stays consistent", () => {
  const { sizes, diagnostics, fractionIterations } = sizeTracks(mixedGrid, fixtureFont);
  assert.equal(sizes.length, 4);
  for (const size of sizes) assert.ok(size >= 0 && Number.isFinite(size));
  assert.deepEqual(diagnostics, []);
  assert.ok(fractionIterations <= 3);
  // hero spans the two fraction tracks: they must fit its min-content width.
  const heroMin = minContentWidth("Gamma delta epsilon", 16, fixtureFont);
  assert.ok(sizes[2] + sizes[3] + mixedGrid.gap >= heroMin - 1e-9);
});
