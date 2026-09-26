import test from 'node:test';
import assert from 'node:assert/strict';
import { HeightIndex } from '../src/heightIndex.ts';
import { mulberry32, randInt } from '../src/rng.ts';

function naivePrefix(heights: number[]): number[] {
  const prefix = [0];
  for (const h of heights) prefix.push(prefix[prefix.length - 1] + h);
  return prefix;
}

test('prefix sums, offsets and total height', () => {
  const idx = new HeightIndex(50);
  idx.insertRows(0, [50, 50, 50]);
  assert.equal(idx.totalHeight(), 150);
  assert.equal(idx.offsetOf(0), 0);
  assert.equal(idx.offsetOf(2), 100);
  idx.setHeight(1, 80);
  assert.equal(idx.totalHeight(), 180);
  assert.equal(idx.offsetOf(2), 130);
});

test('incremental edits match a naive recomputation', () => {
  const rand = mulberry32(42);
  const idx = new HeightIndex(50);
  const model: number[] = [];
  idx.insertRows(0, [50, 50, 50, 50]);
  model.push(50, 50, 50, 50);
  for (let step = 0; step < 300; step++) {
    const op = randInt(rand, 0, 3);
    if (op === 0 && model.length < 60) {
      const at = randInt(rand, 0, model.length);
      const hs = Array.from({ length: randInt(rand, 1, 3) }, () => randInt(rand, 0, 120));
      idx.insertRows(at, hs);
      model.splice(at, 0, ...hs);
    } else if (op === 1 && model.length > 1) {
      const at = randInt(rand, 0, model.length - 1);
      idx.removeRows(at, 1);
      model.splice(at, 1);
    } else if (op === 2 && model.length > 1) {
      const from = randInt(rand, 0, model.length - 1);
      const to = randInt(rand, 0, model.length - 1);
      idx.moveRow(from, to);
      const [h] = model.splice(from, 1);
      model.splice(to, 0, h);
    } else if (model.length > 0) {
      const at = randInt(rand, 0, model.length - 1);
      const h = randInt(rand, 0, 120);
      idx.setHeight(at, h);
      model[at] = h;
    }
    const prefix = naivePrefix(model);
    assert.equal(idx.totalHeight(), prefix[model.length], `total at step ${step}`);
    for (let i = 0; i < model.length; i++) {
      assert.equal(idx.offsetOf(i), prefix[i], `offset ${i} at step ${step}`);
    }
  }
});

test('indexAtOffset is exact and terminates on zero-height runs', () => {
  const idx = new HeightIndex(0);
  idx.insertRows(0, [10, 0, 0, 0, 20, 0, 30]);
  // row tops: 0,10,10,10,10,30,30 -> total 60
  assert.equal(idx.totalHeight(), 60);
  assert.equal(idx.indexAtOffset(-5), 0);
  assert.equal(idx.indexAtOffset(0), 0);
  assert.equal(idx.indexAtOffset(9), 0);
  // tie rule: LAST row whose top <= y (bottom-most of a zero-height run)
  assert.equal(idx.indexAtOffset(10), 4);
  assert.equal(idx.indexAtOffset(29), 4);
  assert.equal(idx.indexAtOffset(30), 6);
  assert.equal(idx.indexAtOffset(59), 6);
  assert.equal(idx.indexAtOffset(60), 6);
  assert.equal(idx.indexAtOffset(10_000), 6);
});

test('all-zero list never loops and resolves deterministically', () => {
  const idx = new HeightIndex(0);
  idx.insertRows(0, [0, 0, 0, 0, 0]);
  assert.equal(idx.totalHeight(), 0);
  assert.equal(idx.indexAtOffset(0), 4);
  assert.equal(idx.indexAtOffset(100), 4);
  idx.removeRows(0, 5);
  assert.equal(idx.indexAtOffset(0), -1);
});

test('version bumps on every mutation', () => {
  const idx = new HeightIndex(50);
  const v0 = idx.version;
  idx.insertRows(0, [50]);
  const v1 = idx.version;
  assert.ok(v1 > v0);
  idx.setHeight(0, 70);
  assert.ok(idx.version > v1);
  const v2 = idx.version;
  idx.setHeight(0, 70); // no-op: same height
  assert.equal(idx.version, v2);
});
