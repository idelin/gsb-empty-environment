import assert from 'node:assert/strict';
import { test } from 'node:test';
import { kernel, makeChain, stage, type StageSpec } from '../src/kernels.ts';
import { InsufficientBudgetError, minimumBudgetBytes, runPipeline } from '../src/pipeline.ts';
import { convolveReference, makeRaster } from '../src/reference.ts';
import {
  UNLIMITED_BUDGET,
  assertRasterExact,
  cleanupDir,
  randomRaster,
  tempWorkDir,
} from './helpers.ts';

const W = 37;
const H = 23;
const TILE = 8;

/** Odd + even taps, off-centre anchors, mixed per-stage boundary policies. */
function spicyChain(version = 1) {
  const stages: StageSpec[] = [
    stage(kernel([0.25, 0.5, 0.25]), kernel([0.2, 0.6, 0.2]), 'clamp', 'clamp'),
    stage(kernel([0.5, 0.25, 0.125, 0.125], 1), kernel([0.4, 0.3, 0.2, 0.1], 2), 'mirror', 'reflect101'),
    stage(kernel([1.5, -0.5], 0), kernel([0.75, 0.25], 1), 'zero', 'wrap'),
  ];
  return makeChain(version, stages);
}

test('fused execution matches untiled per-pixel reference (odd+even chain)', () => {
  const dir = tempWorkDir('tileconv-fused-');
  try {
    const source = randomRaster(W, H);
    const chain = spicyChain();
    const min = minimumBudgetBytes(W, H, TILE, chain, 'fused');
    const result = runPipeline({
      source,
      chain,
      tileSize: TILE,
      mode: 'fused',
      budgetBytes: min.totalBytes, // exact minimum: forces spills
      workDir: dir,
    });
    const expected = convolveReference(source, chain);
    assertRasterExact(result.output, expected, 'fused');
    assert.ok(result.spillCount > 0, 'tight budget must cause spills');
    assert.ok(result.peakMemoryBytes <= min.totalBytes, 'peak must stay within budget');
    assert.ok(result.ops > 0);
  } finally {
    cleanupDir(dir);
  }
});

test('stepwise execution matches untiled per-pixel reference', () => {
  const dir = tempWorkDir('tileconv-step-');
  try {
    const source = randomRaster(W, H);
    const chain = spicyChain();
    const result = runPipeline({
      source,
      chain,
      tileSize: TILE,
      mode: 'stepwise',
      budgetBytes: UNLIMITED_BUDGET,
      workDir: dir,
    });
    assertRasterExact(result.output, convolveReference(source, chain), 'stepwise');
  } finally {
    cleanupDir(dir);
  }
});

test('fused and stepwise agree with each other', () => {
  const dirA = tempWorkDir('tileconv-a-');
  const dirB = tempWorkDir('tileconv-b-');
  try {
    const source = randomRaster(W, H, 7);
    const chain = spicyChain();
    const base = { source, chain, tileSize: TILE, budgetBytes: UNLIMITED_BUDGET };
    const fused = runPipeline({ ...base, mode: 'fused', workDir: dirA });
    const stepwise = runPipeline({ ...base, mode: 'stepwise', workDir: dirB });
    assertRasterExact(fused.output, stepwise.output, 'fused vs stepwise');
  } finally {
    cleanupDir(dirA);
    cleanupDir(dirB);
  }
});

test('width-1 image with mirror boundary and asymmetric kernels terminates and matches', () => {
  const dir = tempWorkDir('tileconv-w1-');
  try {
    const source = randomRaster(1, 9, 3);
    const chain = makeChain(1, [
      stage(kernel([2, 1], 0), kernel([0.5, 0.3, 0.2], 2), 'mirror', 'mirror'),
      stage(kernel([1, 0, 0, -1], 3), kernel([0.5, 0.5], 0), 'reflect101', 'wrap'),
    ]);
    const result = runPipeline({
      source,
      chain,
      tileSize: 4,
      mode: 'fused',
      budgetBytes: UNLIMITED_BUDGET,
      workDir: dir,
    });
    assertRasterExact(result.output, convolveReference(source, chain), 'width-1');
  } finally {
    cleanupDir(dir);
  }
});

test('1x1 image with every boundary policy terminates and matches', () => {
  for (const policy of ['clamp', 'zero', 'wrap', 'mirror', 'reflect101'] as const) {
    const dir = tempWorkDir('tileconv-1x1-');
    try {
      const source = randomRaster(1, 1, 5);
      const chain = makeChain(1, [
        stage(kernel([0.3, 0.4, 0.3], 1), kernel([0.1, 0.9], 0), policy, policy),
      ]);
      const result = runPipeline({
        source,
        chain,
        tileSize: 4,
        mode: 'fused',
        budgetBytes: UNLIMITED_BUDGET,
        workDir: dir,
      });
      assertRasterExact(result.output, convolveReference(source, chain), `1x1 ${policy}`);
    } finally {
      cleanupDir(dir);
    }
  }
});

test('asymmetric kernel is applied in the correct direction (not reversed)', () => {
  const dir = tempWorkDir('tileconv-dir-');
  try {
    const n = 8;
    const data = new Float32Array(n * n * 4);
    data[(4 * n + 3) * 4] = 1; // delta at (3,4), red channel
    const source = makeRaster(n, n, data);
    // out(x) = 2*in(x) + 1*in(x+1): response must appear at x=3 (2) and x=2 (1)
    const chain = makeChain(1, [
      stage(kernel([2, 1], 0), kernel([1], 0), 'zero', 'zero'),
    ]);
    const result = runPipeline({
      source,
      chain,
      tileSize: 3,
      mode: 'fused',
      budgetBytes: UNLIMITED_BUDGET,
      workDir: dir,
    });
    const at = (x: number, y: number) => result.output.data[(y * n + x) * 4];
    assert.equal(at(3, 4), 2);
    assert.equal(at(2, 4), 1);
    assert.equal(at(4, 4), 0, 'reversed kernel would put weight here');
  } finally {
    cleanupDir(dir);
  }
});

test('two chained stages propagate influence exactly two halo layers', () => {
  const dir = tempWorkDir('tileconv-halo2-');
  try {
    const n = 16;
    const data = new Float32Array(n * n * 4);
    data[(8 * n + 8) * 4 + 1] = 1; // delta at (8,8), green channel
    const source = makeRaster(n, n, data);
    const blur = () => stage(kernel([0.25, 0.5, 0.25]), kernel([0.25, 0.5, 0.25]), 'zero', 'zero');
    const chain = makeChain(1, [blur(), blur()]);
    const result = runPipeline({
      source,
      chain,
      tileSize: 4,
      mode: 'fused',
      budgetBytes: UNLIMITED_BUDGET,
      workDir: dir,
    });
    const at = (x: number, y: number) => result.output.data[(y * n + x) * 4 + 1];
    assert.ok(at(10, 8) > 0 && at(8, 10) > 0, 'influence must reach 2 layers');
    assert.equal(at(11, 8), 0, 'influence must not reach 3 layers');
    assert.equal(at(8, 11), 0);
    assert.equal(at(5, 8), 0);
  } finally {
    cleanupDir(dir);
  }
});

test('budget below the exact minimum halo working set is rejected', () => {
  const dir = tempWorkDir('tileconv-budget-');
  try {
    const source = randomRaster(W, H);
    const chain = spicyChain();
    const min = minimumBudgetBytes(W, H, TILE, chain, 'fused');
    assert.throws(
      () =>
        runPipeline({
          source,
          chain,
          tileSize: TILE,
          mode: 'fused',
          budgetBytes: min.totalBytes - 1,
          workDir: dir,
        }),
      (e: unknown) => e instanceof InsufficientBudgetError,
    );
  } finally {
    cleanupDir(dir);
  }
});

test('wrap boundary on narrow tiles matches reference (full-width halo)', () => {
  const dir = tempWorkDir('tileconv-wrap-');
  try {
    const source = randomRaster(13, 11, 11);
    const chain = makeChain(1, [
      stage(kernel([1, 2, 3, 2, 1], 4), kernel([0.5, 0.5], 0), 'wrap', 'wrap'),
      stage(kernel([0.2, 0.6, 0.2], 1), kernel([1, 1, 1], 0), 'wrap', 'mirror'),
    ]);
    const result = runPipeline({
      source,
      chain,
      tileSize: 4,
      mode: 'fused',
      budgetBytes: UNLIMITED_BUDGET,
      workDir: dir,
    });
    assertRasterExact(result.output, convolveReference(source, chain), 'wrap');
  } finally {
    cleanupDir(dir);
  }
});
