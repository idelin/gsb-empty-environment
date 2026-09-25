import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { RGBA } from '../src/rgba.ts';
import {
  cloneImage,
  composeTiles,
  createImage,
  fromStrings,
  getPixel,
  setPixel,
} from '../src/rgba.ts';
import { compareImages } from '../src/diff.ts';

const PAL: Record<string, RGBA> = {
  '.': [0, 0, 0, 0],
  K: [0, 0, 0, 255],
  W: [255, 255, 255, 255],
  R: [255, 0, 0, 255],
  G: [0, 255, 0, 255],
  B: [0, 0, 255, 255],
};

const NO_TOL = { rgb: 0, alpha: 0 } as const;

test('identical images are equal with zero cost and no regions', () => {
  const img = fromStrings(['KWK', 'WKW', 'KWK'], PAL);
  const r = compareImages(img, cloneImage(img), { tolerance: NO_TOL });
  assert.equal(r.equal, true);
  assert.equal(r.cost, 0);
  assert.equal(r.diffPixels, 0);
  assert.equal(r.regions.length, 0);
  assert.equal(r.maxError, 0);
  assert.deepEqual(r.canvas, { x: 0, y: 0, width: 3, height: 3 });
});

test('a single changed pixel is located exactly, mapped to both coordinate systems', () => {
  const a = createImage(4, 4, [0, 0, 0, 255]);
  const b = createImage(4, 4, [0, 0, 0, 255]);
  setPixel(b, 1, 2, [255, 255, 255, 255]);
  // B placed at (1,0): the changed pixel sits at canvas (2,2) => A(2,2), B(1,2).
  const r = compareImages(a, b, { offset: { dx: 1, dy: 0 }, tolerance: NO_TOL });
  assert.equal(r.equal, false);
  // 3 regions: the RGB change plus the two uncovered columns (A-only x=0, B-only x=4).
  assert.equal(r.regions.length, 3);
  const region = r.regions.find((reg) => reg.rgbPixels === 1);
  assert.ok(region);
  assert.equal(region.pixelCount, 1);
  assert.deepEqual(region.bboxCanvas, { x: 2, y: 2, width: 1, height: 1 });
  assert.deepEqual(region.bboxA, { x: 2, y: 2, width: 1, height: 1 });
  assert.deepEqual(region.bboxB, { x: 1, y: 2, width: 1, height: 1 });
  const px = region.pixels[0];
  assert.equal(px.kind, 'rgb');
  assert.deepEqual(px.a, { x: 2, y: 2 });
  assert.deepEqual(px.b, { x: 1, y: 2 });
  assert.deepEqual(px.aValue, [0, 0, 0, 255]);
  assert.deepEqual(px.bValue, [255, 255, 255, 255]);
  assert.equal(r.maxError, 255);
  assert.equal(r.maxRgbDiff, 255);
  assert.equal(r.maxAlphaDiff, 0);
});

test('alpha differences and RGB differences are reported separately', () => {
  const a = createImage(3, 1, [10, 20, 30, 255]);
  const b = createImage(3, 1, [10, 20, 30, 255]);
  setPixel(b, 0, 0, [11, 20, 30, 255]); // RGB only
  setPixel(b, 2, 0, [10, 20, 30, 200]); // alpha only (separated by a gap pixel)
  const r = compareImages(a, b, { tolerance: NO_TOL });
  assert.equal(r.rgbPixels, 1);
  assert.equal(r.alphaPixels, 1);
  assert.equal(r.regions.length, 2);
  const kinds = r.regions.map((reg) => reg.pixels[0].kind).sort();
  assert.deepEqual(kinds, ['alpha', 'rgb']);
  assert.equal(r.maxRgbDiff, 1);
  assert.equal(r.maxAlphaDiff, 55);
  assert.equal(r.maxError, 55);
});

test('tolerance critical points: diff == tolerance passes, tolerance + 1 fails', () => {
  for (const [delta, expectedEqual] of [[10, true], [11, false]] as const) {
    const a = createImage(1, 1, [100, 100, 100, 255]);
    const b = createImage(1, 1, [100 + delta, 100, 100, 255]);
    const r = compareImages(a, b, { tolerance: { rgb: 10, alpha: 0 } });
    assert.equal(r.equal, expectedEqual, `rgb delta ${delta} vs tolerance 10`);
    if (!expectedEqual) {
      assert.equal(r.maxError, 1); // excess above tolerance
      assert.equal(r.cost, 1);
    }
  }
  for (const [delta, expectedEqual] of [[5, true], [6, false]] as const) {
    const a = createImage(1, 1, [7, 7, 7, 100]);
    const b = createImage(1, 1, [7, 7, 7, 100 + delta]);
    const r = compareImages(a, b, { tolerance: { rgb: 0, alpha: 5 } });
    assert.equal(r.equal, expectedEqual, `alpha delta ${delta} vs tolerance 5`);
  }
});

test('transparent pixels: RGB ignored when both transparent; one-sided transparency is an alpha diff', () => {
  // Both transparent with wildly different authored RGB -> equal.
  const a = createImage(5, 1, [0, 0, 0, 0]);
  setPixel(a, 0, 0, [200, 10, 10, 0]);
  const b = createImage(5, 1, [0, 0, 0, 0]);
  setPixel(b, 0, 0, [10, 200, 10, 0]);
  setPixel(b, 2, 0, [0, 0, 0, 1]); // nearly transparent vs fully transparent
  setPixel(b, 4, 0, [255, 0, 0, 255]); // opaque vs transparent
  const r = compareImages(a, b, { tolerance: NO_TOL });
  assert.equal(r.transparentMatches, 3);
  assert.equal(r.diffPixels, 2);
  const byX = [...r.regions].sort((p, q) => p.bboxCanvas.x - q.bboxCanvas.x);
  assert.equal(byX[0].pixels[0].kind, 'alpha'); // alpha 0 vs 1
  assert.equal(byX[0].pixels[0].alphaDiff, 1);
  assert.equal(byX[1].pixels[0].kind, 'rgb+alpha'); // opaque red vs cleaned (0,0,0,0)
  assert.equal(byX[1].pixels[0].error, 255);
});

test('size differences are never cropped: uncovered pixels are first-class diffs with missing evidence', () => {
  const a = createImage(3, 3, [0, 0, 0, 255]); // 9 px
  const b = createImage(5, 2, [0, 0, 0, 255]); // 10 px
  const r = compareImages(a, b, { tolerance: NO_TOL });
  // Union canvas is 5x3; nothing is clipped away.
  assert.deepEqual(r.canvas, { x: 0, y: 0, width: 5, height: 3 });
  assert.equal(r.equal, false);
  assert.equal(r.aOnlyPixels, 3); // A row y=2
  assert.equal(r.bOnlyPixels, 4); // B columns x=3..4
  assert.equal(r.diffPixels, 7);
  assert.equal(r.cost, 7 * 255);
  assert.equal(r.regions.length, 2);
  const aOnly = r.regions.find((reg) => reg.aOnlyPixels > 0);
  const bOnly = r.regions.find((reg) => reg.bOnlyPixels > 0);
  assert.ok(aOnly && bOnly);
  // A-only strip maps to A coordinates and has no B footprint.
  assert.deepEqual(aOnly.bboxA, { x: 0, y: 2, width: 3, height: 1 });
  assert.equal(aOnly.bboxB, null);
  // B-only strip maps to B coordinates and has no A footprint.
  assert.deepEqual(bOnly.bboxB, { x: 3, y: 0, width: 2, height: 2 });
  assert.equal(bOnly.bboxA, null);
  // Missing evidence is preserved per pixel.
  const evidence = aOnly.pixels[0];
  assert.equal(evidence.kind, 'a-only');
  assert.deepEqual(evidence.aValue, [0, 0, 0, 255]);
  assert.equal(evidence.bValue, null);
  assert.equal(evidence.b, null);
});

test('negative offsets: canvas origin goes negative and regions still map back', () => {
  const a = createImage(2, 2, [0, 0, 0, 255]);
  const b = createImage(2, 2, [0, 0, 0, 255]);
  const r = compareImages(a, b, { offset: { dx: -1, dy: -1 }, tolerance: NO_TOL });
  assert.deepEqual(r.canvas, { x: -1, y: -1, width: 3, height: 3 });
  // Overlap is a single pixel (canvas (0,0)): uncovered = 4 + 4 - 2*1 = 6.
  assert.equal(r.aOnlyPixels + r.bOnlyPixels, 6);
  assert.equal(r.cost, 6 * 255);
});

test('same image assembled from tiles in different orders gives identical results', () => {
  const t1 = fromStrings(['RG', 'BW'], PAL);
  const t2 = fromStrings(['KW', 'RK'], PAL);
  const t3 = fromStrings(['BG', 'WR'], PAL);
  const t4 = fromStrings(['GK', 'RB'], PAL);
  const forward = composeTiles(4, 4, [
    { x: 0, y: 0, image: t1 },
    { x: 2, y: 0, image: t2 },
    { x: 0, y: 2, image: t3 },
    { x: 2, y: 2, image: t4 },
  ]);
  const shuffled = composeTiles(4, 4, [
    { x: 2, y: 2, image: t4 },
    { x: 0, y: 2, image: t3 },
    { x: 2, y: 0, image: t2 },
    { x: 0, y: 0, image: t1 },
  ]);
  const expected = fromStrings(['RGKW', 'BWRK', 'BGGK', 'WRRB'], PAL);
  assert.deepEqual([...forward.data], [...expected.data]);
  assert.deepEqual([...shuffled.data], [...forward.data]);
  const r1 = compareImages(forward, shuffled, { tolerance: NO_TOL });
  assert.equal(r1.equal, true);
  assert.equal(r1.cost, 0);
  // And comparisons against a third image are byte-identical regardless of tile order.
  const other = cloneImage(forward);
  setPixel(other, 3, 3, [1, 2, 3, 255]);
  const c1 = compareImages(forward, other, { tolerance: NO_TOL });
  const c2 = compareImages(shuffled, other, { tolerance: NO_TOL });
  assert.deepEqual(c1, c2);
});

test('connected regions: two separated changes form two regions, adjacent changes merge', () => {
  const a = createImage(5, 1, [0, 0, 0, 255]);
  const b = createImage(5, 1, [0, 0, 0, 255]);
  setPixel(b, 0, 0, [9, 9, 9, 255]);
  setPixel(b, 1, 0, [9, 9, 9, 255]); // adjacent to x=0 -> same region
  setPixel(b, 4, 0, [9, 9, 9, 255]); // separated -> its own region
  const r = compareImages(a, b, { tolerance: NO_TOL });
  assert.equal(r.regions.length, 2);
  assert.deepEqual(r.regions[0].bboxA, { x: 0, y: 0, width: 2, height: 1 });
  assert.deepEqual(r.regions[1].bboxA, { x: 4, y: 0, width: 1, height: 1 });
});

test('invalid tolerance is rejected', () => {
  const img = createImage(1, 1);
  assert.throws(() => compareImages(img, img, { tolerance: { rgb: -1, alpha: 0 } }));
  assert.throws(() => compareImages(img, img, { tolerance: { rgb: 0, alpha: 1.5 } }));
});

test('getPixel/setPixel round-trip and bounds checking', () => {
  const img = createImage(2, 2);
  setPixel(img, 1, 1, [1, 2, 3, 4]);
  assert.deepEqual(getPixel(img, 1, 1), [1, 2, 3, 4]);
  assert.throws(() => getPixel(img, 2, 0));
  assert.throws(() => setPixel(img, 0, -1, [0, 0, 0, 0]));
});
