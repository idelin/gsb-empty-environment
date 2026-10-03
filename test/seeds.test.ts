import test from 'node:test';
import assert from 'node:assert/strict';
import { WatershedEngine, WatershedError } from '../src/engine.ts';

function flatEngine(w = 6, h = 6): WatershedEngine {
  return new WatershedEngine(w, h, new Int32Array(w * h).fill(3), { tileSize: 2 });
}

test('duplicate seed id is rejected before any mutation', () => {
  const eng = flatEngine();
  eng.addSeed('A', 1, 1);
  const v = eng.version;
  assert.throws(() => eng.addSeed('A', 2, 2), (e: Error) => e instanceof WatershedError && (e as WatershedError).code === 'DUPLICATE_SEED_ID');
  assert.equal(eng.version, v, 'failed op must not bump the version');
});

test('conflicting seed position is rejected before any mutation', () => {
  const eng = flatEngine();
  eng.addSeed('A', 1, 1);
  const before = eng.getLabels();
  const v = eng.version;
  assert.throws(() => eng.addSeed('B', 1, 1), (e: Error) => (e as WatershedError).code === 'SEED_POSITION_CONFLICT');
  assert.equal(eng.version, v);
  assert.deepEqual([...eng.getLabels()], [...before]);
});

test('moving onto another seed is a conflict, moving onto itself is allowed', () => {
  const eng = flatEngine();
  eng.addSeed('A', 1, 1);
  eng.addSeed('B', 4, 4);
  assert.throws(() => eng.moveSeed('A', 4, 4), (e: Error) => (e as WatershedError).code === 'SEED_POSITION_CONFLICT');
  assert.doesNotThrow(() => eng.moveSeed('A', 1, 1));
});

test('out-of-bounds seeds and heights are rejected', () => {
  const eng = flatEngine();
  assert.throws(() => eng.addSeed('X', -1, 0), (e: Error) => (e as WatershedError).code === 'OUT_OF_BOUNDS');
  assert.throws(() => eng.addSeed('X', 6, 0), (e: Error) => (e as WatershedError).code === 'OUT_OF_BOUNDS');
  assert.throws(() => eng.setHeights([{ x: 0, y: 6, h: 1 }]), (e: Error) => (e as WatershedError).code === 'OUT_OF_BOUNDS');
  assert.throws(() => eng.setHeights([{ x: 0, y: 0, h: 1.5 }]), (e: Error) => (e as WatershedError).code === 'BAD_HEIGHT');
});

test('removing an unknown seed is rejected', () => {
  const eng = flatEngine();
  assert.throws(() => eng.removeSeed('ghost'), (e: Error) => (e as WatershedError).code === 'UNKNOWN_SEED');
});

test('setHeights validates the whole batch before applying anything', () => {
  const eng = flatEngine();
  const v = eng.version;
  assert.throws(() => eng.setHeights([{ x: 0, y: 0, h: 9 }, { x: 5, y: 5, h: 1.2 }]));
  assert.equal(eng.version, v, 'batch must be atomic on validation failure');
});

test('seedless basin stays unassigned (label 0)', () => {
  // Two pits at height 1 separated by a ridge at height 9; only the left pit is seeded.
  const w = 5, h = 5;
  const profile = [1, 2, 9, 2, 1];
  const heights = new Int32Array(w * h);
  for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) heights[y * w + x] = profile[x];
  const eng = new WatershedEngine(w, h, heights, { tileSize: 2 });
  const la = eng.addSeed('A', 0, 0);
  for (let y = 0; y < h; y++) {
    assert.equal(eng.getLabel(0, y), la);
    assert.equal(eng.getLabel(1, y), la);
    assert.equal(eng.getLabel(2, y), 0, 'ridge tie goes to the unseeded minimum front');
    assert.equal(eng.getLabel(3, y), 0, 'right basin stays unassigned');
    assert.equal(eng.getLabel(4, y), 0, 'right basin stays unassigned');
  }
});

test('image with no seeds at all is fully unassigned', () => {
  const eng = new WatershedEngine(4, 3, Int32Array.from({ length: 12 }, (_, i) => i % 5), { tileSize: 2 });
  assert.ok([...eng.getLabels()].every(l => l === 0));
  assert.equal(eng.getAdjacency().length, 0);
});
