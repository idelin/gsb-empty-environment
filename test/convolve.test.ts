import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chainHalo, makeKernel } from '../src/kernel.ts';
import { convolveReference, planChain, runFused, runStepwise } from '../src/convolve.ts';
import { ArraySource, Raster } from '../src/raster.ts';
import { Stats } from '../src/stats.ts';
import { TileStore } from '../src/store.ts';
import { makeImage, maxDiff } from './helpers.ts';

function setup(tileSize: number, budget = 1 << 26) {
  const dir = mkdtempSync(join(tmpdir(), 'conv-'));
  const stats = new Stats();
  const store = new TileStore(dir, budget, tileSize * tileSize * 16, stats);
  return { stats, store };
}

// 奇核(3) + 偶核(4) 串联, 混合边界策略
const kernels = [
  makeKernel({ id: 'k1', hx: [0.25, 0.5, 0.25], hy: [0.25, 0.5, 0.25], anchorX: 1, anchorY: 1, boundaryX: 'clamp', boundaryY: 'mirror' }),
  makeKernel({ id: 'k2', hx: [0.1, 0.2, 0.4, 0.3], hy: [0.3, 0.4, 0.2, 0.1], anchorX: 2, anchorY: 0, boundaryX: 'wrap', boundaryY: 'clamp' }),
];

test('奇偶核串联: 分块融合/分步 与未分块逐像素参考一致', () => {
  const W = 13;
  const H = 11;
  const TILE = 4;
  const img = makeImage(W, H);
  const ref = convolveReference(img, W, H, kernels);

  const { stats, store } = setup(TILE);
  const out = new Raster('out', store, W, H, TILE);
  runFused(new ArraySource(img, W, H), out, kernels, stats);
  const fused = out.readRegion(0, 0, W, H);
  assert.ok(maxDiff(fused, ref) < 1e-9, `fused maxDiff=${maxDiff(fused, ref)}`);

  const step = runStepwise(new ArraySource(img, W, H), W, H, TILE, kernels, store, stats, 't1');
  assert.ok(maxDiff(step, ref) < 1e-9, `stepwise maxDiff=${maxDiff(step, ref)}`);
  store.dispose();
});

test('halo 推导: 串联总 halo = 各级之和, 区域规划与之一致', () => {
  const halo = chainHalo(kernels);
  assert.deepEqual(halo, { left: 3, right: 2, top: 1, bottom: 4 });
  const out = { x0: 8, y0: 8, w: 4, h: 4 };
  const plan = planChain(out, 32, 32, kernels);
  assert.deepEqual(plan.regions[2], out);
  assert.deepEqual(plan.regions[1], { x0: 6, y0: 8, w: 7, h: 7 });
  assert.deepEqual(plan.regions[0], { x0: 5, y0: 7, w: 9, h: 9 });
});

test('宽度为 1 + 镜像边界 + 非对称核: 不死循环且与参考一致', () => {
  const W = 1;
  const H = 7;
  const TILE = 2;
  const ks = [
    makeKernel({ id: 'a', hx: [0.5, 0.3, 0.2], hy: [0.4, 0.6], anchorX: 0, anchorY: 1, boundaryX: 'mirror', boundaryY: 'mirror' }),
    makeKernel({ id: 'b', hx: [0.7, 0.3], hy: [0.1, 0.2, 0.7], anchorX: 1, anchorY: 0, boundaryX: 'mirror', boundaryY: 'wrap' }),
  ];
  const img = makeImage(W, H);
  const ref = convolveReference(img, W, H, ks);
  const { stats, store } = setup(TILE);
  const out = new Raster('o', store, W, H, TILE);
  runFused(new ArraySource(img, W, H), out, ks, stats);
  assert.ok(maxDiff(out.readRegion(0, 0, W, H), ref) < 1e-9);
  const step = runStepwise(new ArraySource(img, W, H), W, H, TILE, ks, store, stats, 'w1');
  assert.ok(maxDiff(step, ref) < 1e-9);
  store.dispose();
});

test('非对称核方向不反转', () => {
  const W = 8;
  const H = 1;
  const img = new Float32Array(W * H * 4);
  for (let x = 0; x < W; x++) img[x * 4] = x + 1;
  // out(x) = in(x+2): 若核被反转会得到 in(x-2)
  const k = makeKernel({ id: 'shift', hx: [0, 0, 1], hy: [1], anchorX: 0, anchorY: 0, boundaryX: 'zero', boundaryY: 'clamp' });
  const { stats, store } = setup(3);
  const out = new Raster('o', store, W, H, 3);
  runFused(new ArraySource(img, W, H), out, [k], stats);
  const res = out.readRegion(0, 0, W, H);
  for (let x = 0; x < W; x++) {
    const expect = x + 2 < W ? x + 3 : 0;
    assert.equal(res[x * 4], expect, `x=${x}`);
  }
  store.dispose();
});

test('锚点越界抛出 RangeError', () => {
  assert.throws(
    () => makeKernel({ id: 'bad', hx: [1, 1, 1], hy: [1], anchorX: 3, anchorY: 0 }),
    RangeError,
  );
});
