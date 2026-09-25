import { test } from "node:test";
import assert from "node:assert/strict";

import {
  FIXTURE_GLYPH_METRICS,
  measureText,
} from "../src/glyph-metrics.ts";
import {
  FIXTURE_CONTAINER_WIDTH,
  FIXTURE_GAP,
  FIXTURE_TRACKS,
  FIXTURE_TREE,
} from "../src/fixtures.ts";
import { layout } from "../src/layout.ts";
import { sizeTracks } from "../src/tracks.ts";
import type { GridItem } from "../src/tree.ts";

const item = (
  id: string,
  columnStart: number,
  columnSpan: number,
  minWidth: number,
): GridItem => ({ id, columnStart, columnSpan, minWidth });

const sum = (values: readonly number[]): number =>
  values.reduce((a, b) => a + b, 0);

test("glyph metrics are deterministic and fixture-driven", () => {
  assert.equal(measureText("ii", FIXTURE_GLYPH_METRICS), 6);
  assert.equal(measureText("WW", FIXTURE_GLYPH_METRICS), 30);
  assert.equal(measureText("????", FIXTURE_GLYPH_METRICS), 32); // default advance
  assert.equal(
    measureText("North", FIXTURE_GLYPH_METRICS),
    measureText("North", FIXTURE_GLYPH_METRICS),
  );
});

test("fixed tracks keep their exact size", () => {
  const result = sizeTracks({
    tracks: [
      { kind: "fixed", size: 100 },
      { kind: "fixed", size: 50 },
      { kind: "proportional", weight: 1 },
    ],
    items: [],
    containerWidth: 400,
  });
  assert.equal(result.sizes[0], 100);
  assert.equal(result.sizes[1], 50);
});

test("content tracks size to the largest contained item minimum", () => {
  const result = sizeTracks({
    tracks: [
      { kind: "content" },
      { kind: "proportional", weight: 1 },
    ],
    items: [item("a", 0, 1, 40), item("b", 0, 1, 75), item("c", 1, 1, 10)],
    containerWidth: 300,
  });
  assert.equal(result.sizes[0], 75);
});

test("proportional tracks split free space by weight", () => {
  const result = sizeTracks({
    tracks: [
      { kind: "fixed", size: 100 },
      { kind: "content" },
      { kind: "proportional", weight: 1 },
      { kind: "proportional", weight: 1 },
    ],
    items: [item("a", 1, 1, 60)],
    containerWidth: 500,
  });
  // base = 160, free = 340, split evenly
  assert.deepEqual(result.sizes, [100, 60, 170, 170]);
  assert.equal(result.usedWidth, 500);
  assert.equal(result.diagnostics.length, 0);
});

test("spanning item minimum width constrains the total spanned width", () => {
  const result = sizeTracks({
    tracks: [
      { kind: "fixed", size: 50 },
      { kind: "content" },
      { kind: "content" },
    ],
    items: [
      item("span-01", 0, 2, 150),
      item("span-12", 1, 2, 200),
    ],
    containerWidth: 260,
    gap: 10,
  });
  assert.ok(result.converged);
  const span01 = result.sizes[0] + result.sizes[1] + 10;
  const span12 = result.sizes[1] + result.sizes[2] + 10;
  assert.ok(span01 >= 150, `span 0-1 total ${span01} >= 150`);
  assert.ok(span12 >= 200, `span 1-2 total ${span12} >= 200`);
  // Exact fixpoint: [50, 140, 50]
  assert.deepEqual(result.sizes, [50, 140, 50]);
});

test("span deficit distribution prefers growable tracks over fixed ones", () => {
  const result = sizeTracks({
    tracks: [
      { kind: "fixed", size: 50 },
      { kind: "content" },
    ],
    items: [item("wide", 0, 2, 150)],
    containerWidth: 300,
    gap: 10,
  });
  // deficit = 150 - (50 + 0 + 10) = 90, absorbed by the content track alone
  assert.deepEqual(result.sizes, [50, 90]);
});

test("span across proportional tracks still constrains the final total", () => {
  const result = sizeTracks({
    tracks: [
      { kind: "proportional", weight: 1 },
      { kind: "proportional", weight: 1 },
    ],
    items: [item("wide", 0, 2, 300)],
    containerWidth: 400,
    gap: 10,
  });
  assert.ok(result.converged);
  assert.equal(sum(result.sizes) + 10, 400);
  assert.ok(sum(result.sizes) + 10 >= 300);
});

test("iterative distribution converges and is idempotent", () => {
  const input = {
    tracks: [
      { kind: "content" },
      { kind: "content" },
      { kind: "content" },
    ],
    items: [
      item("s1", 0, 2, 120),
      item("s2", 1, 2, 180),
      item("s3", 0, 3, 260),
    ],
    containerWidth: 600,
    gap: 8,
  } as const;
  const first = sizeTracks(input);
  const second = sizeTracks(input);
  assert.ok(first.converged);
  assert.ok(first.iterations <= 100);
  assert.deepEqual([...first.sizes], [...second.sizes]);
  // Every span constraint satisfied at the fixpoint.
  for (const it of input.items) {
    let spanned = 8 * (it.columnSpan - 1);
    for (let i = it.columnStart; i < it.columnStart + it.columnSpan; i++) {
      spanned += first.sizes[i];
    }
    assert.ok(spanned >= it.minWidth - 1e-9, `${it.id} constraint holds`);
  }
});

test("insufficient container reports overflow instead of negative widths", () => {
  const result = sizeTracks({
    tracks: [
      { kind: "fixed", size: 100 },
      { kind: "fixed", size: 100 },
    ],
    items: [],
    containerWidth: 150,
    gap: 20,
  });
  const overflow = result.diagnostics.find((d) => d.kind === "overflow");
  assert.ok(overflow, "overflow diagnostic present");
  assert.equal(overflow.kind === "overflow" && overflow.amount, 70);
  assert.deepEqual(result.sizes, [100, 100]);
  for (const size of result.sizes) assert.ok(size >= 0);
});

test("zero-weight proportional tracks never divide by zero", () => {
  const result = sizeTracks({
    tracks: [
      { kind: "proportional", weight: 0 },
      { kind: "proportional", weight: 0 },
    ],
    items: [],
    containerWidth: 300,
  });
  for (const size of result.sizes) {
    assert.ok(Number.isFinite(size));
    assert.equal(size, 0);
  }
  const undistributed = result.diagnostics.find(
    (d) => d.kind === "undistributed-free-space",
  );
  assert.ok(undistributed, "undistributed free space reported");
  assert.equal(
    undistributed.kind === "undistributed-free-space" && undistributed.amount,
    300,
  );
});

test("a zero-weight track next to a positive-weight track gets nothing", () => {
  const result = sizeTracks({
    tracks: [
      { kind: "fixed", size: 100 },
      { kind: "proportional", weight: 0 },
      { kind: "proportional", weight: 2 },
    ],
    items: [],
    containerWidth: 400,
  });
  assert.deepEqual(result.sizes, [100, 0, 300]);
  assert.equal(result.diagnostics.length, 0);
});

test("gaps are reflected in positions and used width", () => {
  const result = sizeTracks({
    tracks: [
      { kind: "fixed", size: 100 },
      { kind: "fixed", size: 50 },
      { kind: "fixed", size: 30 },
    ],
    items: [],
    containerWidth: 500,
    gap: 10,
  });
  assert.deepEqual(result.positions, [0, 110, 170]);
  assert.equal(result.usedWidth, 200);
});

test("out-of-range spans are diagnosed, not crashed on", () => {
  const result = sizeTracks({
    tracks: [{ kind: "content" }, { kind: "content" }],
    items: [item("lost", 1, 5, 100)],
    containerWidth: 300,
  });
  assert.ok(
    result.diagnostics.some(
      (d) => d.kind === "span-out-of-range" && d.itemId === "lost",
    ),
  );
  assert.ok(result.converged);
});

test("fixture tree lays out to exactly the container width", () => {
  const result = layout({
    tree: FIXTURE_TREE,
    tracks: FIXTURE_TRACKS,
    containerWidth: FIXTURE_CONTAINER_WIDTH,
    gap: FIXTURE_GAP,
  });
  assert.ok(result.converged);
  assert.equal(result.diagnostics.length, 0);
  assert.ok(
    Math.abs(result.usedWidth - FIXTURE_CONTAINER_WIDTH) < 1e-9,
    `used ${result.usedWidth} fills ${FIXTURE_CONTAINER_WIDTH}`,
  );
  // Spanning banner (cols 1-2) minimum width holds against final sizes.
  const bannerMin =
    measureText("Summary: growth across all regions", FIXTURE_GLYPH_METRICS) +
    16;
  const bannerSpan = result.sizes[1] + result.sizes[2] + FIXTURE_GAP;
  assert.ok(bannerSpan >= bannerMin, `banner span ${bannerSpan} >= ${bannerMin}`);
  // Proportional columns share the free space 2:1 on top of their bases.
  const free =
    FIXTURE_CONTAINER_WIDTH - sum(result.baseSizes) - FIXTURE_GAP * 3;
  assert.ok(
    Math.abs(result.sizes[2] - (result.baseSizes[2] + (free * 2) / 3)) < 1e-9,
  );
  assert.ok(
    Math.abs(result.sizes[3] - (result.baseSizes[3] + free / 3)) < 1e-9,
  );
});

test("fixture tree in a tiny container overflows cleanly", () => {
  const result = layout({
    tree: FIXTURE_TREE,
    tracks: FIXTURE_TRACKS,
    containerWidth: 200,
    gap: FIXTURE_GAP,
  });
  assert.ok(result.diagnostics.some((d) => d.kind === "overflow"));
  for (const size of result.sizes) assert.ok(size >= 0);
});
