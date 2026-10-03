import test from 'node:test';
import assert from 'node:assert/strict';
import { runSample, sampleOps } from '../src/samples.ts';
import { decodeRgbaToHeights, sampleTerrainRgba } from '../src/rgba.ts';
import { WatershedEngine } from '../src/engine.ts';
import { replay } from '../src/ops.ts';
import { segmentReference } from '../src/reference.ts';
import { mulberry32, shuffler } from './helpers.ts';

test('sample replay is deterministic', () => {
  assert.equal(runSample(), runSample());
});

test('sample replay is immune to random tile order', () => {
  const plain = runSample();
  const shuffled = runSample(shuffler(mulberry32(2024)));
  assert.equal(shuffled, plain);
});

test('sample ends fully committed and equal to the reference flood', () => {
  const { buf, width, height } = sampleTerrainRgba();
  const heights = decodeRgbaToHeights(buf, width, height);
  const eng = new WatershedEngine(width, height, heights, { tileSize: 8 });
  replay(eng, sampleOps());
  assert.equal(eng.paused, false);
  const ref = segmentReference(width, height, eng.getHeights(), eng.getSeeds());
  assert.deepEqual([...eng.getLabels()], [...ref.labels]);
});

test('sample transcript shows patches, events and incremental stats', () => {
  const t = runSample();
  assert.match(t, /addSeed\(A@8,8\) -> v1 patch=\d+px pops=\d+ tiles=\d+ exp=\d+ split\(0->1\)/);
  assert.match(t, /removeSeed\(C\) -> v\d+ patch=\d+px .*merge\(/);
  assert.match(t, /paused \(still v\d+\)/, 'budget-limited step must pause');
  assert.match(t, /resume -> v\d+/);
  assert.match(t, /adjacency /);
});
