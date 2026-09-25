import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  createImage,
  getPixel,
  mulberry32,
  randomImage,
  setPixel,
} from '../src/rgba.ts';
import { compareImages } from '../src/diff.ts';
import type { AlignOptions } from '../src/align.ts';
import {
  bruteForceAlignment,
  buildIntervalPyramid,
  exactCost,
  lowerBoundCost,
  searchAlignment,
} from '../src/align.ts';

const NO_TOL = { rgb: 0, alpha: 0 } as const;

function offsetsOf(result: { optima: readonly { dx: number; dy: number }[] }): string[] {
  return result.optima.map((o) => `${o.dx},${o.dy}`).sort();
}

test('exactCost agrees with the full compareImages cost (independent implementations)', () => {
  const rand = mulberry32(42);
  for (let trial = 0; trial < 10; trial++) {
    const a = randomImage(1 + Math.floor(rand() * 6), 1 + Math.floor(rand() * 6), rand, 0.25);
    const b = randomImage(1 + Math.floor(rand() * 6), 1 + Math.floor(rand() * 6), rand, 0.25);
    const tol = { rgb: Math.floor(rand() * 8), alpha: Math.floor(rand() * 8) };
    for (let dy = -3; dy <= 3; dy++) {
      for (let dx = -3; dx <= 3; dx++) {
        const lean = exactCost(a, b, { dx, dy }, tol, 255);
        const full = compareImages(a, b, { offset: { dx, dy }, tolerance: tol }).cost;
        assert.equal(lean, full, `cost mismatch at offset ${dx},${dy}`);
      }
    }
  }
});

test('lower bound is admissible on every offset and exact at level 0', () => {
  const rand = mulberry32(7);
  for (let trial = 0; trial < 10; trial++) {
    const a = randomImage(1 + Math.floor(rand() * 7), 1 + Math.floor(rand() * 7), rand, 0.3);
    const b = randomImage(1 + Math.floor(rand() * 7), 1 + Math.floor(rand() * 7), rand, 0.3);
    const tol = { rgb: Math.floor(rand() * 6), alpha: Math.floor(rand() * 6) };
    const penalty = trial % 2 === 0 ? 255 : 17;
    const levels = 2;
    const pyrA = buildIntervalPyramid(a, levels);
    const pyrB = buildIntervalPyramid(b, levels);
    for (let dy = -4; dy <= 4; dy++) {
      for (let dx = -4; dx <= 4; dx++) {
        const offset = { dx, dy };
        const exact = exactCost(a, b, offset, tol, penalty);
        const lb0 = lowerBoundCost(pyrA, pyrB, a, b, offset, tol, penalty, 0);
        assert.equal(lb0, exact, `level-0 bound must equal exact cost at ${dx},${dy}`);
        for (const level of [1, 2]) {
          const lb = lowerBoundCost(pyrA, pyrB, a, b, offset, tol, penalty, level);
          assert.ok(lb <= exact, `level-${level} bound ${lb} exceeds exact ${exact} at ${dx},${dy}`);
        }
      }
    }
  }
});

test('shifted sub-image: unique optimum at the true offset, cost = uncovered penalty only', () => {
  const rand = mulberry32(1234);
  const a = randomImage(8, 8, rand);
  const b = createImage(6, 6);
  for (let y = 0; y < 6; y++) {
    for (let x = 0; x < 6; x++) {
      setPixel(b, x, y, getPixel(a, x + 2, y + 1));
    }
  }
  const options: AlignOptions = {
    range: { minDX: -4, maxDX: 4, minDY: -4, maxDY: 4 },
    tolerance: NO_TOL,
  };
  const result = searchAlignment(a, b, options);
  assert.deepEqual(offsetsOf(result), ['2,1']);
  // B fully inside A: uncovered = 64 + 36 - 2*36 = 28 pixels, zero pixel error.
  assert.equal(result.bestCost, 28 * 255);
  const brute = bruteForceAlignment(a, b, options);
  assert.deepEqual(offsetsOf(brute), offsetsOf(result));
  assert.equal(brute.bestCost, result.bestCost);
});

test('repeated texture: every same-cost optimal offset is reported', () => {
  // Pattern with exact period (4, 2); the 4x2 fundamental domain is unique.
  const a = createImage(8, 8);
  for (let y = 0; y < 8; y++) {
    for (let x = 0; x < 8; x++) {
      const u = x % 4;
      const v = y % 2;
      setPixel(a, x, y, [u * 60 + 30, v * 120 + 10, (u * 2 + v) * 25, 255]);
    }
  }
  const b = createImage(8, 8);
  for (let y = 0; y < 8; y++) {
    for (let x = 0; x < 8; x++) {
      setPixel(b, x, y, getPixel(a, (x + 4) % 8, y)); // B == A shifted by 4 in x
    }
  }
  const options: AlignOptions = {
    range: { minDX: -4, maxDX: 4, minDY: -2, maxDY: 2 },
    tolerance: NO_TOL,
    uncoveredPenalty: 0, // isolate pixel-error ties from uncovered cost
  };
  const result = searchAlignment(a, b, options);
  const expected = [
    '-4,-2', '-4,0', '-4,2',
    '0,-2', '0,0', '0,2',
    '4,-2', '4,0', '4,2',
  ];
  assert.equal(result.bestCost, 0);
  assert.deepEqual(offsetsOf(result), expected);
  const brute = bruteForceAlignment(a, b, options);
  assert.deepEqual(offsetsOf(brute), expected);
});

test('pruned search matches brute-force reference on random images (no missed optima)', () => {
  const rand = mulberry32(20260926);
  for (let trial = 0; trial < 25; trial++) {
    const a = randomImage(1 + Math.floor(rand() * 8), 1 + Math.floor(rand() * 8), rand, 0.25);
    const b = randomImage(1 + Math.floor(rand() * 8), 1 + Math.floor(rand() * 8), rand, 0.25);
    const options: AlignOptions = {
      range: { minDX: -3, maxDX: 3, minDY: -3, maxDY: 3 },
      tolerance: { rgb: Math.floor(rand() * 6), alpha: Math.floor(rand() * 6) },
      uncoveredPenalty: trial % 3 === 0 ? 0 : 255,
      levels: 2,
    };
    const pruned = searchAlignment(a, b, options);
    const brute = bruteForceAlignment(a, b, options);
    assert.equal(pruned.bestCost, brute.bestCost, `trial ${trial}: bestCost`);
    assert.deepEqual(offsetsOf(pruned), offsetsOf(brute), `trial ${trial}: optima`);
    const d = pruned.diagnostics;
    assert.equal(d.evaluated + d.pruned, d.totalOffsets);
  }
});

test('pruning actually prunes and never drops tied optima', () => {
  const a = randomImage(8, 8, mulberry32(99));
  const options: AlignOptions = {
    range: { minDX: -4, maxDX: 4, minDY: -4, maxDY: 4 },
    tolerance: NO_TOL,
  };
  const result = searchAlignment(a, a, options);
  assert.equal(result.bestCost, 0);
  assert.deepEqual(offsetsOf(result), ['0,0']); // uncovered penalty breaks all other ties
  assert.ok(result.diagnostics.pruned > 0, 'expected some offsets to be pruned');
  assert.ok(result.diagnostics.evaluated < result.diagnostics.totalOffsets);
  // levels: 0 (pixel-level bound, still exact) must give the same answer
  const unpruned = searchAlignment(a, a, { ...options, levels: 0 });
  assert.deepEqual(offsetsOf(unpruned), offsetsOf(result));
  assert.equal(
    unpruned.diagnostics.evaluated + unpruned.diagnostics.pruned,
    unpruned.diagnostics.totalOffsets,
  );
});

test('tolerance decides the alignment outcome at the critical point', () => {
  const a = createImage(4, 1, [0, 0, 0, 255]);
  const b = createImage(4, 1, [0, 0, 0, 255]);
  setPixel(b, 0, 0, [10, 0, 0, 255]); // red channel off by exactly 10
  const range = { minDX: -1, maxDX: 1, minDY: 0, maxDY: 0 };
  const strict = searchAlignment(a, b, { range, tolerance: { rgb: 10, alpha: 0 } });
  assert.equal(strict.bestCost, 0); // delta == tolerance -> still a match
  const tighter = searchAlignment(a, b, { range, tolerance: { rgb: 9, alpha: 0 } });
  assert.equal(tighter.bestCost, 1); // excess of 1 at the best offset (dx=0)
  assert.deepEqual(offsetsOf(tighter), ['0,0']);
});

test('different sizes: alignment cost includes uncovered pixels, nothing hidden', () => {
  // 2x2 white vs 4x4 white: best alignment still pays for 12 uncovered pixels.
  const small = createImage(2, 2, [255, 255, 255, 255]);
  const big = createImage(4, 4, [255, 255, 255, 255]);
  const options: AlignOptions = {
    range: { minDX: -2, maxDX: 2, minDY: -2, maxDY: 2 },
    tolerance: NO_TOL,
  };
  const result = searchAlignment(small, big, options);
  // small fully inside big: uncovered = 4 + 16 - 2*4 = 12 -> cost 12*255.
  assert.equal(result.bestCost, 12 * 255);
  // B (big) placed at (dx,dy) covers A (small, at origin) iff dx,dy in {-2,-1,0}.
  assert.deepEqual(
    offsetsOf(result),
    ['-1,-1', '-1,-2', '-1,0', '-2,-1', '-2,-2', '-2,0', '0,-1', '0,-2', '0,0'],
  );
});
