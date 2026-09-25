/**
 * End-to-end sample: build a small RGBA image in code (no Canvas, no image
 * libraries), quantize it deterministically, and exercise every feature:
 * tiled streaming invariance, frontier save/restore, transparent barriers,
 * revision affected-region analysis, and the exact baseline gap report.
 *
 * Run: node examples/sample.ts   (Node >= 22.6)
 */

import {
  SRGB_U8_V1,
  affectedRegion,
  buildPalette,
  createState,
  makeImage,
  quantizeFull,
  quantizeImage,
  quantizeTiled,
  restoreState,
  setPixel,
  snapshotState,
  TRANSPARENT_INDEX,
  type RGBAImage,
} from "../src/index.ts";

function buildSampleImage(): RGBAImage {
  const w = 16;
  const h = 12;
  const img = makeImage(w, h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const r = Math.round((x / (w - 1)) * 255);
      const g = Math.round((y / (h - 1)) * 255);
      const b = Math.round(((x + y) / (w + h - 2)) * 255);
      setPixel(img, x, y, r, g, b, 255);
    }
  }
  // Transparent barrier column at x = 8 (full height) ...
  for (let y = 0; y < h; y++) setPixel(img, 8, y, 0, 0, 0, 0);
  // ... and a transparent block in the bottom-left corner.
  for (let y = 9; y < h; y++) for (let x = 0; x < 3; x++) setPixel(img, x, y, 0, 0, 0, 0);
  return img;
}

function fnv1a(indices: Int32Array): string {
  let hash = 0x811c9dc5;
  for (const v of indices) {
    hash ^= v & 0xff;
    hash = Math.imul(hash, 0x01000193) >>> 0;
  }
  return hash.toString(16).padStart(8, "0");
}

function renderMap(img: RGBAImage, indices: Int32Array): string {
  const glyphs = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ";
  const lines: string[] = [];
  for (let y = 0; y < img.height; y++) {
    let line = "";
    for (let x = 0; x < img.width; x++) {
      const idx = indices[y * img.width + x];
      line += idx === TRANSPARENT_INDEX ? "." : glyphs[idx % glyphs.length];
    }
    lines.push(line);
  }
  return lines.join("\n");
}

const image = buildSampleImage();
const opaque = image.data.filter((_, i) => i % 4 === 3 && image.data[i] !== 0).length;
console.log(`image: ${image.width}x${image.height}, opaque pixels: ${opaque}, transparent: ${image.width * image.height - opaque}`);

// --- Main pipeline: 4 opaque colors, dithered, streamed in 4-row tiles ---
const report = quantizeImage(image, { colors: 4, dither: true, tileHeight: 4 });
console.log("\npalette (opaque colors only):");
report.palette.colors.forEach((c, i) => console.log(`  [${i}] rgb(${c.join(", ")})`));
console.log(`\nindexed map (. = transparent):\n${renderMap(image, report.indexed.indices)}`);
console.log("\nreconstruction error (transparent pixels excluded):");
console.log(`  mse=${report.error.mse.toFixed(4)} rmse=${report.error.rmse.toFixed(4)} mae=${report.error.mae.toFixed(4)} max=${report.error.maxError.toFixed(1)}`);

if (report.baseline) {
  const b = report.baseline;
  console.log("\nexact baseline (small color domain):");
  console.log(`  distinct opaque colors=${b.distinctColors}, partitions enumerated=${b.partitions}`);
  console.log(`  exact min assignment error = ${b.error.toFixed(4)}`);
  console.log(`  heuristic assignment error = ${b.heuristicError.toFixed(4)}`);
  console.log(`  gap = ${b.gap.toFixed(4)} (${(b.gapRelative * 100).toFixed(2)}% above exact; heuristic, not claimed optimal)`);
}

// --- Tile-size invariance ---
console.log("\ntile-size invariance (hash of indices must be identical):");
for (const th of [1, 2, 3, 4, 5, 12]) {
  const r = quantizeImage(image, { colors: 4, dither: true, tileHeight: th, exactBaselineLimit: 0 });
  console.log(`  tileHeight=${String(th).padStart(2)}  hash=${fnv1a(r.indexed.indices)}`);
}

// --- Frontier save/restore: pause after row 4, resume from JSON ---
const palette = buildPalette(image, 4);
const part1 = createState(image.width, SRGB_U8_V1.version);
const head = quantizeTiled(image, palette, SRGB_U8_V1, { dither: true, tileHeight: 4, state: part1 });
const firstTile = head.next().value!;
const saved = JSON.stringify(snapshotState(part1));
const resumed = restoreState(JSON.parse(saved));
const rest = quantizeFull(image, palette, SRGB_U8_V1, { dither: true, tileHeight: 4, state: resumed });
rest.indexed.indices.set(firstTile.indices, 0);
const whole = quantizeFull(image, palette, SRGB_U8_V1, { dither: true, tileHeight: 4 });
console.log(`\nfrontier save/restore: resumed hash=${fnv1a(rest.indexed.indices)} uninterrupted hash=${fnv1a(whole.indexed.indices)} (saved ${saved.length} bytes of JSON, colorSpace=${resumed.colorSpaceVersion})`);

// --- Determinism: identical inputs, identical outputs ---
const again = quantizeImage(image, { colors: 4, dither: true, tileHeight: 4, exactBaselineLimit: 0 });
console.log(`\ndeterminism: run1=${fnv1a(report.indexed.indices)} run2=${fnv1a(again.indexed.indices)}`);

// --- Revision affected region: edit one pixel, see the propagation wedge ---
const rect = { x: 5, y: 3, width: 1, height: 1 };
const spans = affectedRegion(image, rect, true);
console.log(`\nrevision at (${rect.x},${rect.y}): affected spans per row (note the barrier column x=8 clips the wedge):`);
for (const s of spans) console.log(`  y=${String(s.y).padStart(2)}  x=[${s.x0}..${s.x1}]`);

// --- colorCount = 1 is defined ---
const one = quantizeImage(image, { colors: 1, dither: false, exactBaselineLimit: 0 });
console.log(`\ncolors=1: palette=${JSON.stringify(one.palette.colors)} rmse=${one.error.rmse.toFixed(4)}`);

// --- Exact baseline on a small color domain ---
const small = makeImage(4, 3);
const few: [number, number, number][] = [
  [10, 10, 10], [20, 15, 10], [15, 10, 20], [200, 50, 60],
  [210, 55, 50], [195, 60, 70], [50, 200, 90], [60, 210, 80],
  [55, 195, 100], [12, 12, 12], [205, 52, 55], [52, 205, 85],
];
few.forEach((c, i) => setPixel(small, i % 4, Math.floor(i / 4), c[0], c[1], c[2]));
setPixel(small, 3, 2, 0, 0, 0, 0); // one transparent pixel: excluded from the fit
const sr = quantizeImage(small, { colors: 2, dither: false });
console.log(`\nsmall domain (11 distinct opaque colors, K=2):`);
console.log(`  heuristic palette: ${JSON.stringify(sr.palette.colors)}`);
console.log(`  exact optimal palette: ${JSON.stringify(sr.baseline!.palette.colors)}`);
console.log(`  exact min error=${sr.baseline!.error}  heuristic error=${sr.baseline!.heuristicError}  gap=${sr.baseline!.gap} (${(sr.baseline!.gapRelative * 100).toFixed(2)}%)`);
