import { test } from "node:test";
import assert from "node:assert/strict";
import {
  buildPalette,
  makeImage,
  medianCut,
  paletteSse,
  solveExact,
  distinctOpaqueColors,
} from "../src/index.ts";

const T = [0, 0, 0, 0] as const;

test("colorCount=1 has a defined single weighted-mean color", () => {
  const img = makeImage(2, 2, [
    [0, 0, 0, 255],
    [10, 20, 30, 255],
    [200, 100, 0, 255],
    [0, 0, 0, 0],
  ]);
  const result = buildPalette(img, { colorCount: 1 });
  assert.equal(result.palette.length, 1);
  assert.deepEqual(result.palette[0], [70, 40, 10]);
  assert.equal(result.certifiedBest, true);
  assert.equal(result.method, "exact");
});

test("transparent pixels do not consume opaque color budget", () => {
  const img = makeImage(1, 3, [
    [10, 10, 10, 255],
    [200, 200, 200, 0],
    [20, 20, 20, 255],
  ]);
  const result = buildPalette(img, { colorCount: 2 });
  assert.equal(result.distinctOpaqueColors, 2);
  assert.equal(result.sse, 0);
});

test("image with no opaque pixels yields a defined all-zero palette", () => {
  const img = makeImage(2, 2, [T, T, T, T]);
  const result = buildPalette(img, { colorCount: 4 });
  assert.equal(result.palette.length, 4);
  assert.deepEqual(result.palette, [
    [0, 0, 0],
    [0, 0, 0],
    [0, 0, 0],
    [0, 0, 0],
  ]);
});

test("exact solver matches independent brute force on a tiny domain", () => {
  const colors = [
    [10, 10, 10],
    [12, 12, 12],
    [200, 200, 200],
    [205, 205, 205],
    [100, 30, 250],
  ] as const;
  const weights = [3, 1, 4, 2, 5];
  const exact = solveExact(colors, weights, 2);

  const m = colors.length;
  const blockBest = (members: number[]) => {
    const lo = [255, 255, 255];
    const hi = [0, 0, 0];
    for (const i of members) {
      for (let c = 0; c < 3; c++) {
        lo[c] = Math.min(lo[c]!, colors[i]![c]!);
        hi[c] = Math.max(hi[c]!, colors[i]![c]!);
      }
    }
    let bestBlock = Infinity;
    for (let r = lo[0]!; r <= hi[0]!; r++) {
      for (let g = lo[1]!; g <= hi[1]!; g++) {
        for (let b = lo[2]!; b <= hi[2]!; b++) {
          let sse = 0;
          for (const i of members) {
            const dr = colors[i]![0] - r;
            const dg = colors[i]![1] - g;
            const db = colors[i]![2] - b;
            sse += weights[i]! * (dr * dr + dg * dg + db * db);
          }
          if (sse < bestBlock) bestBlock = sse;
        }
      }
    }
    return bestBlock;
  };
  let best = Infinity;
  for (let mask = 1; mask < (1 << m) - 1; mask++) {
    const a: number[] = [];
    const b: number[] = [];
    for (let i = 0; i < m; i++) (mask & (1 << i) ? a : b).push(i);
    best = Math.min(best, blockBest(a) + blockBest(b));
  }
  assert.equal(exact.sse, best);
});

test("median cut is deterministic and reports gap without claiming optimal", () => {
  const pixels = [];
  for (let i = 0; i < 40; i++) pixels.push([i * 6, 0, 0, 255]);
  for (let i = 0; i < 40; i++) pixels.push([0, i * 6, 0, 255]);
  const img = makeImage(80, 1, pixels);

  const a = buildPalette(img, { colorCount: 4, forceHeuristic: true });
  const b = buildPalette(img, { colorCount: 4, forceHeuristic: true });
  assert.equal(a.fingerprint, b.fingerprint);
  assert.equal(a.method, "median-cut");
  assert.equal(a.certifiedBest, false);
  assert.equal(a.gapVsExact, null);
  assert.equal(a.exactSse, null);

  const small = makeImage(1, 6, [
    [0, 0, 0, 255],
    [1, 1, 1, 255],
    [100, 100, 100, 255],
    [102, 102, 102, 255],
    [250, 250, 250, 255],
    [252, 252, 252, 255],
  ]);
  const h = buildPalette(small, { colorCount: 2, forceHeuristic: true });
  assert.equal(h.certifiedBest, false);
  assert.notEqual(h.exactSse, null);
  assert.ok(h.gapVsExact !== null && h.gapVsExact >= 0);
  assert.ok(h.sse >= h.exactSse!);
});

test("exact result never worse than median cut", () => {
  const colors = [
    [0, 0, 0],
    [9, 9, 9],
    [100, 40, 10],
    [110, 42, 12],
    [240, 240, 10],
    [235, 235, 0],
  ];
  const weights = [1, 1, 1, 1, 1, 1];
  const exact = solveExact(colors, weights, 3);
  const heur = medianCut(colors, weights, 3);
  const img = makeImage(6, 1, colors.map((c) => [...c, 255] as const));
  const heurSse = paletteSse(img, heur);
  assert.ok(exact.sse <= heurSse);
});

test("distinct color extraction ignores transparent pixels", () => {
  const img = makeImage(2, 2, [
    [5, 5, 5, 255],
    [5, 5, 5, 10],
    [9, 9, 9, 255],
    [9, 9, 9, 255],
  ]);
  const entries = distinctOpaqueColors(img);
  assert.equal(entries.length, 2);
});
