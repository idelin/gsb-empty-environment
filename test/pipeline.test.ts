import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { makeKernel } from '../src/kernel.ts';
import { convolveReference, minBudgetBytes } from '../src/convolve.ts';
import { BudgetError, CancelError, Pipeline, VersionConflictError } from '../src/pipeline.ts';
import type { ExecutionMode } from '../src/convolve.ts';
import type { SeparableKernel } from '../src/types.ts';
import { makeImage, maxDiff, mulberry32 } from './helpers.ts';

const W = 16;
const H = 16;
const TILE = 4;
const BIG = 1 << 26;
const INPUT = makeImage(W, H, 7);

// 两级核: halo (1,1,1,1) + (2,2,0,2) = 总 (3,3,1,3)
const kernelsA = [
  makeKernel({ id: 'a1', hx: [0.25, 0.5, 0.25], hy: [0.25, 0.5, 0.25], anchorX: 1, anchorY: 1, boundaryX: 'clamp', boundaryY: 'clamp' }),
  makeKernel({ id: 'a2', hx: [0.1, 0.2, 0.4, 0.2, 0.1], hy: [0.3, 0.4, 0.3], anchorX: 2, anchorY: 0, boundaryX: 'clamp', boundaryY: 'clamp' }),
];

function makePipeline(mode: ExecutionMode, budget: number, kernels: SeparableKernel[] = kernelsA): Pipeline {
  const dir = mkdtempSync(join(tmpdir(), 'pipe-'));
  return new Pipeline({ dir, width: W, height: H, tileSize: TILE, budgetBytes: budget, mode, kernels }, INPUT);
}

test('单 tile 修改: 两层 halo 传播, 局部重算与全量一致', async () => {
  const p = makePipeline('fused', BIG);
  const full = await p.compute();
  assert.equal(full.stats.tilesComputed, 16);

  const rnd = mulberry32(99);
  const tileData = new Float32Array(TILE * TILE * 4);
  for (let i = 0; i < tileData.length; i++) tileData[i] = rnd();
  const affected = p.updateTile(1, 1, tileData);
  // 总 halo (3,3,1,3): tile(1,1) 覆盖 x[4,8) y[4,8) -> 扩展 x[1,11) y[3,11) -> tile 列 0..2 行 0..2
  assert.equal(affected.length, 9);

  const res = await p.compute();
  assert.equal(res.stats.tilesComputed - full.stats.tilesComputed, 9, '只局部重算受影响的 9 个 tile');

  const input2 = Float32Array.from(INPUT);
  for (let y = 0; y < TILE; y++) {
    for (let x = 0; x < TILE; x++) {
      for (let c = 0; c < 4; c++) {
        input2[((TILE + y) * W + TILE + x) * 4 + c] = tileData[(y * TILE + x) * 4 + c];
      }
    }
  }
  const ref = convolveReference(input2, W, H, kernelsA);
  assert.ok(maxDiff(res.image, ref) < 1e-9, `maxDiff=${maxDiff(res.image, ref)}`);
  p.dispose();
});

test('核热替换: 计算期间换核抛 VersionConflict, 失败重试不混用新旧核版本', async () => {
  const p = makePipeline('fused', BIG);
  const kernelsB = [
    makeKernel({ id: 'b1', hx: [0.5, 0.5], hy: [0.5, 0.5], anchorX: 0, anchorY: 0, boundaryX: 'mirror', boundaryY: 'mirror' }),
  ];
  let swapped = false;
  await assert.rejects(
    p.compute({
      onTileComputed: () => {
        if (!swapped) {
          swapped = true;
          p.swapKernels(kernelsB);
        }
      },
    }),
    VersionConflictError,
  );
  const res = await p.compute();
  const ref = convolveReference(INPUT, W, H, kernelsB);
  assert.ok(maxDiff(res.image, ref) < 1e-9, '重试结果必须完全来自新核');
  const index = JSON.parse(readFileSync(p.indexPath, 'utf8'));
  assert.equal(index.kernelVersion, 1);
  assert.deepEqual(index.kernels, ['b1']);
  p.dispose();
});

test('任务取消: 释放缓存与临时文件, 已发布索引保持不变', async () => {
  const p = makePipeline('fused', BIG);
  const ok = await p.compute();
  const indexBefore = readFileSync(p.indexPath, 'utf8');

  p.setInput(INPUT); // 标记全部待算, 使下一轮有活可干
  const ac = new AbortController();
  await assert.rejects(
    p.compute({ signal: ac.signal, onTileComputed: () => ac.abort() }),
    CancelError,
  );
  assert.equal(readFileSync(p.indexPath, 'utf8'), indexBefore, '失败不得污染已发布索引');
  assert.equal(readdirSync(p.spillDir).length, 0, '临时溢写文件已释放');
  assert.equal(p.stats.cacheBytes, 0, '缓存已释放');

  const again = await p.compute();
  assert.ok(maxDiff(again.image, ok.image) < 1e-9);
  p.dispose();
});

test('预算小于最小 halo 需求时明确拒绝, 恰好满足则可运行且峰值不超预算', async () => {
  const need = minBudgetBytes(W, H, TILE, kernelsA, 'fused');
  assert.throws(
    () => makePipeline('fused', need - 1),
    (err: unknown) => err instanceof BudgetError && /预算不足/.test((err as Error).message),
  );
  const p = makePipeline('fused', need);
  const res = await p.compute();
  const ref = convolveReference(INPUT, W, H, kernelsA);
  assert.ok(maxDiff(res.image, ref) < 1e-9);
  assert.ok(res.stats.peakBytes <= need, `峰值 ${res.stats.peakBytes} 应 <= 预算 ${need}`);
  p.dispose();
});

test('分步模式小预算触发溢写且结果正确', async () => {
  const need = minBudgetBytes(W, H, TILE, kernelsA, 'stepwise');
  const p = makePipeline('stepwise', need);
  const res = await p.compute();
  const ref = convolveReference(INPUT, W, H, kernelsA);
  assert.ok(maxDiff(res.image, ref) < 1e-9);
  assert.ok(res.stats.spillCount > 0, '应发生溢写');
  assert.ok(res.stats.peakBytes <= need, `峰值 ${res.stats.peakBytes} 应 <= 预算 ${need}`);
  p.dispose();
});
