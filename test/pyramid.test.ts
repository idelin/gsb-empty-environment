import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  ImagePyramid,
  binomialKernel5,
  mirrorIndex,
  type Kernel,
} from '../src/pyramid.ts';

// ---------- helpers ----------

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomImage(width: number, height: number, rng: () => number): Float64Array {
  const data = new Float64Array(width * height);
  for (let i = 0; i < data.length; i++) data[i] = rng() * 255;
  return data;
}

/** Independent naive full recompute used as ground truth. */
function referenceDownsample(
  prev: Float64Array,
  pw: number,
  ph: number,
  kernel: Kernel,
): { data: Float64Array; width: number; height: number } {
  const w = Math.max(1, Math.ceil(pw / 2));
  const h = Math.max(1, Math.ceil(ph / 2));
  const r = (kernel.size - 1) / 2;
  const out = new Float64Array(w * h);
  for (let py = 0; py < h; py++) {
    for (let px = 0; px < w; px++) {
      let sum = 0;
      for (let ky = 0; ky < kernel.size; ky++) {
        const sy = mirrorIndex(2 * py + ky - r, ph);
        for (let kx = 0; kx < kernel.size; kx++) {
          sum += kernel.weights[ky * kernel.size + kx] * prev[sy * pw + mirrorIndex(2 * px + kx - r, pw)];
        }
      }
      out[py * w + px] = sum;
    }
  }
  return { data: out, width: w, height: h };
}

function referencePyramid(
  base: Float64Array,
  width: number,
  height: number,
  levelCount: number,
  kernel: Kernel,
): Float64Array[] {
  const levels = [base.slice()];
  let pw = width;
  let ph = height;
  for (let li = 1; li < levelCount; li++) {
    const next = referenceDownsample(levels[li - 1], pw, ph, kernel);
    levels.push(next.data);
    pw = next.width;
    ph = next.height;
  }
  return levels;
}

function assertMatchesReference(pyr: ImagePyramid, ref: Float64Array[]): void {
  assert.equal(pyr.levelCount, ref.length);
  for (let li = 0; li < ref.length; li++) {
    assert.deepStrictEqual(pyr.getLevelPixels(li), ref[li], `level ${li} mismatch`);
  }
}

function applyEdit(
  base: Float64Array,
  width: number,
  x: number,
  y: number,
  w: number,
  h: number,
  data: Float64Array,
): void {
  for (let row = 0; row < h; row++) {
    for (let col = 0; col < w; col++) {
      base[(y + row) * width + x + col] = data[row * w + col];
    }
  }
}

const KERNEL5 = binomialKernel5();
const KERNEL3: Kernel = {
  size: 3,
  weights: [0.05, 0.2, 0.05, 0.2, 0.4, 0.2, 0.05, 0.2, 0.05],
};

// ---------- tests ----------

test('mirrorIndex implements reflect-101 extension', () => {
  assert.equal(mirrorIndex(0, 5), 0);
  assert.equal(mirrorIndex(4, 5), 4);
  assert.equal(mirrorIndex(-1, 5), 1);
  assert.equal(mirrorIndex(-2, 5), 2);
  assert.equal(mirrorIndex(5, 5), 3);
  assert.equal(mirrorIndex(6, 5), 2);
  assert.equal(mirrorIndex(-4, 5), 4);
  assert.equal(mirrorIndex(-5, 5), 3); // -5 -> 5 -> 3
  assert.equal(mirrorIndex(9, 5), 1); // 9 -> 1
  assert.equal(mirrorIndex(-3, 1), 0);
  assert.equal(mirrorIndex(7, 1), 0);
});

test('full build matches reference on non-block-aligned image', () => {
  const rng = mulberry32(42);
  const width = 37;
  const height = 29;
  const base = randomImage(width, height, rng);
  const pyr = new ImagePyramid({ width, height, blockSize: 8, kernel: KERNEL5 });
  pyr.edit(0, 0, width, height, base);

  assert.equal(pyr.isClean, false);
  assert.equal(pyr.update(), true);
  assert.equal(pyr.isClean, true);

  const ref = referencePyramid(base, width, height, pyr.levelCount, KERNEL5);
  assertMatchesReference(pyr, ref);
  for (let li = 0; li < pyr.levelCount; li++) {
    assert.equal(pyr.dirtyBlockCount(li), 0);
    assert.equal(pyr.pendingDirtyRect(li), null);
  }
});

test('precise invalidation ranges propagate with kernel-radius expansion', () => {
  const pyr = new ImagePyramid({ width: 64, height: 64, blockSize: 16, levels: 4, kernel: KERNEL5 });
  pyr.edit(0, 0, 64, 64, new Float64Array(64 * 64).fill(1));
  pyr.update();

  // Edit rect [13,29) x [5,9) with kernel radius 2.
  pyr.edit(13, 5, 16, 4, new Float64Array(16 * 4).fill(2));

  // Level 1 (32x32): px in [ceil((13-2)/2), floor((29-1+2)/2)] = [6, 15],
  // py in [ceil((5-2)/2), floor((9-1+2)/2)] = [2, 5].
  assert.deepStrictEqual(pyr.pendingDirtyRect(1), { x: 6, y: 2, w: 10, h: 4 });
  // Level 2 (16x16): derived from [6,16) x [2,6) -> [2,9) x [0,4).
  assert.deepStrictEqual(pyr.pendingDirtyRect(2), { x: 2, y: 0, w: 7, h: 4 });
  // Level 3 (8x8): derived from [2,9) x [0,4) -> [0,6) x [0,3).
  assert.deepStrictEqual(pyr.pendingDirtyRect(3), { x: 0, y: 0, w: 6, h: 3 });
  // Base level is written directly, never pending.
  assert.equal(pyr.pendingDirtyRect(0), null);
  assert.equal(pyr.dirtyBlockCount(0), 0);

  // Block snapping: level 1 dirty pixels x 6..15 / y 2..5 touch only block 0.
  assert.equal(pyr.dirtyBlockCount(1), 1);
  assert.equal(pyr.dirtyBlockCount(2), 1);
  assert.equal(pyr.dirtyBlockCount(3), 1);

  // A rect one pixel further right would cross into the next level-1 block.
  pyr.update();
  pyr.edit(19, 5, 16, 4, new Float64Array(16 * 4).fill(3));
  // level 1 x range: [ceil(17/2), floor((35-1+2)/2)] = [9, 18] -> blocks 0 and 1.
  assert.deepStrictEqual(pyr.pendingDirtyRect(1), { x: 9, y: 2, w: 10, h: 4 });
  assert.equal(pyr.dirtyBlockCount(1), 2);
});

test('cross-block local edit recomputes exactly the dirty blocks', () => {
  const rng = mulberry32(7);
  const width = 40;
  const height = 40;
  const base = randomImage(width, height, rng);
  const pyr = new ImagePyramid({ width, height, blockSize: 8, levels: 4, kernel: KERNEL5 });
  pyr.edit(0, 0, width, height, base);
  pyr.update();

  // Rect straddling block boundaries (blocks are 8px wide).
  const patch = randomImage(7, 11, rng);
  pyr.edit(6, 7, 7, 11, patch);
  applyEdit(base, width, 6, 7, 7, 11, patch);

  let pending = 0;
  for (let li = 0; li < pyr.levelCount; li++) pending += pyr.dirtyBlockCount(li);
  assert.ok(pending > 0);
  const computedBefore = pyr.stats.computedBlocks;

  assert.equal(pyr.update(), true);
  assert.equal(pyr.stats.computedBlocks - computedBefore, pending);
  assert.equal(pyr.stats.rejectedCommits, 0);

  const ref = referencePyramid(base, width, height, pyr.levelCount, KERNEL5);
  assertMatchesReference(pyr, ref);
});

test('edits at borders use mirror extension', () => {
  const rng = mulberry32(11);
  const width = 33;
  const height = 17;
  const base = randomImage(width, height, rng);
  const pyr = new ImagePyramid({ width, height, blockSize: 8, kernel: KERNEL3 });
  pyr.edit(0, 0, width, height, base);
  pyr.update();

  const corner = randomImage(3, 2, rng);
  pyr.edit(0, 0, 3, 2, corner);
  applyEdit(base, width, 0, 0, 3, 2, corner);

  const farEdge = randomImage(3, 3, rng);
  pyr.edit(30, 14, 3, 3, farEdge);
  applyEdit(base, width, 30, 14, 3, 3, farEdge);

  assert.equal(pyr.update(), true);
  const ref = referencePyramid(base, width, height, pyr.levelCount, KERNEL3);
  assertMatchesReference(pyr, ref);
});

test('two consecutive incomplete update rounds converge to reference', () => {
  const rng = mulberry32(23);
  const width = 48;
  const height = 48;
  const base = randomImage(width, height, rng);
  const pyr = new ImagePyramid({ width, height, blockSize: 8, levels: 4, kernel: KERNEL5 });
  pyr.edit(0, 0, width, height, base);
  pyr.update();

  // Round 1: edit, then only partially update.
  const patchA = randomImage(8, 8, rng);
  pyr.edit(4, 4, 8, 8, patchA);
  applyEdit(base, width, 4, 4, 8, 8, patchA);
  assert.equal(pyr.update(2), false);
  assert.equal(pyr.isClean, false);

  // Round 2: a second overlapping edit lands before round 1 finished;
  // again only a partial update runs.
  const patchB = randomImage(12, 8, rng);
  pyr.edit(10, 10, 12, 8, patchB);
  applyEdit(base, width, 10, 10, 12, 8, patchB);
  assert.equal(pyr.update(3), false);
  assert.equal(pyr.isClean, false);

  // Drain the queue; every level must match a from-scratch recompute.
  assert.equal(pyr.update(), true);
  const ref = referencePyramid(base, width, height, pyr.levelCount, KERNEL5);
  assertMatchesReference(pyr, ref);
  assert.equal(pyr.stats.rejectedCommits, 0);
});

test('stale in-flight result never overwrites newer parent blocks', () => {
  const rng = mulberry32(31);
  const width = 48;
  const height = 48;
  const base = randomImage(width, height, rng);
  const pyr = new ImagePyramid({ width, height, blockSize: 8, levels: 4, kernel: KERNEL5 });
  pyr.edit(0, 0, width, height, base);
  pyr.update();

  const patchA = randomImage(16, 16, rng);
  pyr.edit(8, 8, 16, 16, patchA);
  applyEdit(base, width, 8, 8, 16, 16, patchA);

  // Simulate a concurrent edit landing while a level-2 block is in flight:
  // it re-invalidates the very block being computed plus its dependencies.
  const patchB = randomImage(16, 16, rng);
  let fired = false;
  pyr.onBlockComputed = (level) => {
    if (!fired && level === 2) {
      fired = true;
      pyr.edit(8, 8, 16, 16, patchB);
      applyEdit(base, width, 8, 8, 16, 16, patchB);
    }
  };

  assert.equal(pyr.update(), true);
  pyr.onBlockComputed = null;

  // The in-flight level-2 block was computed from stale inputs and its
  // commit must have been rejected exactly once, then recomputed.
  assert.equal(pyr.stats.rejectedCommits, 1);

  const ref = referencePyramid(base, width, height, pyr.levelCount, KERNEL5);
  assertMatchesReference(pyr, ref);
});

test('rejected commit also triggers on dependency-only changes', () => {
  const rng = mulberry32(53);
  const width = 48;
  const height = 48;
  const base = randomImage(width, height, rng);
  const pyr = new ImagePyramid({ width, height, blockSize: 8, levels: 4, kernel: KERNEL5 });
  pyr.edit(0, 0, width, height, base);
  pyr.update();

  const patchA = randomImage(16, 16, rng);
  pyr.edit(0, 0, 16, 16, patchA);
  applyEdit(base, width, 0, 0, 16, 16, patchA);

  // Edit lands mid-update but only touches the child footprint of the
  // in-flight block, not the block's own pixel range directly... the
  // precise invalidation still re-marks it; either way the guard holds.
  const patchB = randomImage(4, 4, rng);
  let fired = false;
  pyr.onBlockComputed = (level) => {
    if (!fired && level === 1) {
      fired = true;
      pyr.edit(0, 0, 4, 4, patchB);
      applyEdit(base, width, 0, 0, 4, 4, patchB);
    }
  };

  assert.equal(pyr.update(), true);
  pyr.onBlockComputed = null;
  assert.ok(pyr.stats.rejectedCommits >= 1);

  const ref = referencePyramid(base, width, height, pyr.levelCount, KERNEL5);
  assertMatchesReference(pyr, ref);
});

test('input validation', () => {
  const pyr = new ImagePyramid({ width: 16, height: 16 });
  assert.throws(() => pyr.edit(-1, 0, 4, 4, new Float64Array(16)), RangeError);
  assert.throws(() => pyr.edit(0, 0, 20, 4, new Float64Array(80)), RangeError);
  assert.throws(() => pyr.edit(0, 0, 4, 4, new Float64Array(3)), RangeError);
  assert.throws(() => new ImagePyramid({ width: 0, height: 4 }), RangeError);
  assert.throws(() => new ImagePyramid({ width: 4, height: 4, blockSize: 0 }), RangeError);
  assert.throws(
    () => new ImagePyramid({ width: 4, height: 4, kernel: { size: 2, weights: [1, 1, 1, 1] } }),
    RangeError,
  );
});
