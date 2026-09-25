/**
 * End-to-end sample: build two small RGBA images by hand, find the alignment
 * offset exactly, then diff at the best offset and inspect the regions.
 *
 * Run: node --experimental-strip-types examples/demo.ts   (Node >= 22.6)
 */

import { createImage, getPixel, setPixel } from '../src/rgba.ts';
import { compareImages } from '../src/diff.ts';
import { bruteForceAlignment, searchAlignment } from '../src/align.ts';

// --- Build image A: a 16x12 opaque pattern ---------------------------------
const A = createImage(16, 12, [0, 0, 0, 255]);
for (let y = 0; y < A.height; y++) {
  for (let x = 0; x < A.width; x++) {
    setPixel(A, x, y, [(x * 16) % 256, (y * 21) % 256, ((x + y) * 9) % 256, 255]);
  }
}

// --- Build image B: A shifted by (3,2), cropped to 13x10, plus 2 edits -----
const B = createImage(13, 10);
for (let y = 0; y < B.height; y++) {
  for (let x = 0; x < B.width; x++) {
    setPixel(B, x, y, getPixel(A, x + 3, y + 2));
  }
}
setPixel(B, 4, 3, [250, 10, 10, 255]); // one RGB change
const before = getPixel(B, 7, 6);
setPixel(B, 7, 6, [before[0], before[1], before[2], 120]); // one alpha change

const tolerance = { rgb: 4, alpha: 4 };
const range = { minDX: -6, maxDX: 6, minDY: -6, maxDY: 6 };

// --- 1. Alignment search (pruned) + independent brute-force reference ------
const pruned = searchAlignment(A, B, { range, tolerance });
const brute = bruteForceAlignment(A, B, { range, tolerance });

console.log('=== alignment search ===');
console.log(`range: dx in [${range.minDX}, ${range.maxDX}], dy in [${range.minDY}, ${range.maxDY}]`);
console.log(`best cost: ${pruned.bestCost}`);
console.log(`optimal offsets (all ties): ${pruned.optima.map((o) => `(${o.dx},${o.dy})`).join(' ')}`);
const d = pruned.diagnostics;
console.log(
  `diagnostics: ${d.totalOffsets} offsets, ${d.evaluated} evaluated exactly, ` +
    `${d.pruned} pruned by lower bounds (pyramid levels: ${d.levels})`,
);
const bruteOptima = brute.optima.map((o) => `(${o.dx},${o.dy})`).join(' ');
console.log(`brute-force reference: best=${brute.bestCost}, optima=${bruteOptima}`);
console.log(`cross-check: ${brute.bestCost === pruned.bestCost && bruteOptima === pruned.optima.map((o) => `(${o.dx},${o.dy})`).join(' ') ? 'MATCH' : 'MISMATCH'}`);

// Cost map around the optimum (from the brute-force reference).
console.log('\ncost map (rows = dy, cols = dx, around optimum):');
const header = ['dy\\dx', ...Array.from({ length: 7 }, (_, i) => String(i))].join('\t');
console.log(header);
for (let dy = 0; dy <= 4; dy++) {
  const cells = [`${dy}  `];
  for (let dx = 0; dx <= 6; dx++) {
    const c = brute.costs.find((k) => k.dx === dx && k.dy === dy);
    cells.push(String(c?.cost ?? '-'));
  }
  console.log(cells.join('\t'));
}

// --- 2. Diff at the best offset --------------------------------------------
const best = pruned.optima[0];
const cmp = compareImages(A, B, { offset: best, tolerance });
console.log(`\n=== diff at offset (${best.dx},${best.dy}) with tolerance rgb<=${tolerance.rgb} alpha<=${tolerance.alpha} ===`);
console.log(
  `canvas (union, nothing cropped): x=${cmp.canvas.x} y=${cmp.canvas.y} ` +
    `${cmp.canvas.width}x${cmp.canvas.height}`,
);
console.log(
  `diff pixels: ${cmp.diffPixels} (rgb=${cmp.rgbPixels}, alpha=${cmp.alphaPixels}, ` +
    `A-only=${cmp.aOnlyPixels}, B-only=${cmp.bOnlyPixels})`,
);
console.log(
  `maxError=${cmp.maxError}, maxRgbDiff=${cmp.maxRgbDiff}, maxAlphaDiff=${cmp.maxAlphaDiff}, cost=${cmp.cost}`,
);
console.log(`connected regions: ${cmp.regions.length}`);
for (const r of cmp.regions) {
  const fmt = (b: { x: number; y: number; width: number; height: number } | null) =>
    b ? `(${b.x},${b.y} ${b.width}x${b.height})` : 'none';
  console.log(
    `  region #${r.id}: canvas=${fmt(r.bboxCanvas)} A=${fmt(r.bboxA)} B=${fmt(r.bboxB)} ` +
      `pixels=${r.pixelCount} rgb=${r.rgbPixels} alpha=${r.alphaPixels} ` +
      `aOnly=${r.aOnlyPixels} bOnly=${r.bOnlyPixels} maxError=${r.maxError}`,
  );
}

// Show the two edited pixels with both coordinate mappings.
console.log('\nedited pixels (mapped back to both images):');
for (const r of cmp.regions) {
  for (const p of r.pixels) {
    if (p.kind === 'rgb' || p.kind === 'alpha' || p.kind === 'rgb+alpha') {
      console.log(
        `  kind=${p.kind} canvas=(${p.canvas.x},${p.canvas.y}) ` +
          `A=${JSON.stringify(p.a)} B=${JSON.stringify(p.b)} ` +
          `aValue=${JSON.stringify(p.aValue)} bValue=${JSON.stringify(p.bValue)} ` +
          `rgbDiff=${p.rgbDiff} alphaDiff=${p.alphaDiff}`,
      );
    }
  }
}

// Sample of missing evidence (A-only border pixels where B does not reach).
const aOnlyRegion = cmp.regions.find((r) => r.aOnlyPixels > 0);
if (aOnlyRegion) {
  const p = aOnlyRegion.pixels[0];
  console.log('\nmissing-evidence sample (A-only pixel):');
  console.log(
    `  canvas=(${p.canvas.x},${p.canvas.y}) A=${JSON.stringify(p.a)} aValue=${JSON.stringify(p.aValue)} ` +
      `B=${JSON.stringify(p.b)} bValue=${JSON.stringify(p.bValue)}`,
  );
}
