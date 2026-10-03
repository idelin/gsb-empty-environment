import test from 'node:test';
import assert from 'node:assert/strict';
import { buildRgba, decodeRgbaToHeights, sampleTerrainRgba } from '../src/rgba.ts';
import { WatershedEngine } from '../src/engine.ts';
import { segmentReference } from '../src/reference.ts';

test('RGBA round-trip: built image decodes to the expected luminance heightmap', () => {
  const w = 6, h = 4;
  const buf = buildRgba(w, h, (x, y) => [x * 10, y * 20, 100, 255]);
  const heights = decodeRgbaToHeights(buf, w, h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const expect = ((x * 10) * 299 + (y * 20) * 587 + 100 * 114) / 1000 | 0;
      assert.equal(heights[y * w + x], expect);
    }
  }
});

test('decoder rejects truncated buffers and non-opaque pixels', () => {
  assert.throws(() => decodeRgbaToHeights(new Uint8Array(7), 2, 1), /size/);
  const buf = buildRgba(2, 1, () => [0, 0, 0, 255]);
  buf[3] = 128;
  assert.throws(() => decodeRgbaToHeights(buf, 2, 1), /alpha/);
});

test('segmentation runs on the self-built RGBA terrain and matches the reference', () => {
  const { buf, width, height } = sampleTerrainRgba();
  const heights = decodeRgbaToHeights(buf, width, height);
  const eng = new WatershedEngine(width, height, heights, { tileSize: 8 });
  const la = eng.addSeed('A', 8, 8);
  const lb = eng.addSeed('B', 24, 16);
  // The crater is a seedless basin: its bottom stays unassigned.
  assert.equal(eng.getLabel(22, 6), 0);
  assert.equal(eng.getLabel(8, 8), la);
  assert.equal(eng.getLabel(24, 16), lb);
  const ref = segmentReference(width, height, heights, eng.getSeeds());
  assert.deepEqual([...eng.getLabels()], [...ref.labels]);
  // Sanity: the two seeded basins are non-trivial and adjacent to unassigned land.
  const adj = eng.getAdjacency();
  assert.ok(adj.some(e => e.a === 0 && e.b === la));
  assert.ok(adj.some(e => e.a === 0 && e.b === lb));
});
