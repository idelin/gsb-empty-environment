import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chainHalo, makeKernel, stageHalo } from '../src/kernel.ts';
import { convolveReference, minBudgetBytes } from '../src/convolve.ts';
import { BudgetError, Pipeline } from '../src/pipeline.ts';
import { Stats } from '../src/stats.ts';
import { CHANNELS } from '../src/types.ts';
import { makeImage, maxDiff, mulberry32 } from '../test/helpers.ts';

const W = 48;
const H = 36;
const TILE = 8;

// 奇核 + 偶核 + 非对称核串联, 每级不同边界策略
const kernels = [
  makeKernel({ id: 'blur3', hx: [0.25, 0.5, 0.25], hy: [0.25, 0.5, 0.25], anchorX: 1, anchorY: 1, boundaryX: 'clamp', boundaryY: 'clamp' }),
  makeKernel({ id: 'sharpen4', hx: [-0.25, 0.75, 0.75, -0.25], hy: [0.5, 0.5], anchorX: 2, anchorY: 0, boundaryX: 'mirror', boundaryY: 'mirror' }),
  makeKernel({ id: 'trail3', hx: [0.6, 0.3, 0.1], hy: [0.2, 0.8], anchorX: 0, anchorY: 1, boundaryX: 'zero', boundaryY: 'clamp' }),
];

console.log('== 核链与 halo 推导 ==');
for (const k of kernels) console.log(`  ${k.id}: halo=${JSON.stringify(stageHalo(k))} 边界=(${k.boundaryX},${k.boundaryY})`);
console.log(`  融合执行总 halo: ${JSON.stringify(chainHalo(kernels))}`);

const input = makeImage(W, H, 2024);
const dir = mkdtempSync(join(tmpdir(), 'convdemo-'));
console.log(`\n工作目录: ${dir}`);

// 1) 未分块逐像素参考
const refStats = new Stats();
const ref = convolveReference(input, W, H, kernels, refStats);

// 2) 融合分块
const minFused = minBudgetBytes(W, H, TILE, kernels, 'fused');
const fused = new Pipeline(
  { dir: join(dir, 'fused'), width: W, height: H, tileSize: TILE, budgetBytes: minFused * 4, mode: 'fused', kernels },
  input,
);
const fusedRes = await fused.compute();

// 3) 分步(最小预算 -> 强制溢写)
const minStep = minBudgetBytes(W, H, TILE, kernels, 'stepwise');
const step = new Pipeline(
  { dir: join(dir, 'step'), width: W, height: H, tileSize: TILE, budgetBytes: minStep, mode: 'stepwise', kernels },
  input,
);
const stepRes = await step.compute();

console.log('\n== 正确性 ==');
console.log(`  fused    vs 参考 maxDiff = ${maxDiff(fusedRes.image, ref).toExponential(2)}`);
console.log(`  stepwise vs 参考 maxDiff = ${maxDiff(stepRes.image, ref).toExponential(2)}`);

console.log('\n== 计算量与峰值内存 ==');
console.log(`  参考(未分块): macs=${refStats.macs}`);
console.log(`  融合:  macs=${fusedRes.stats.macs}  peak=${fusedRes.stats.peakBytes}B  spill=${fusedRes.stats.spillCount}  (最小预算 ${minFused}B)`);
console.log(`  分步:  macs=${stepRes.stats.macs}  peak=${stepRes.stats.peakBytes}B  spill=${stepRes.stats.spillCount}  load=${stepRes.stats.loadCount}  (最小预算 ${minStep}B)`);

// 4) 单 tile 修改 -> 链式 halo 传播 -> 局部重算
const rnd = mulberry32(7);
const tileData = new Float32Array(TILE * TILE * CHANNELS);
for (let i = 0; i < tileData.length; i++) tileData[i] = rnd();
const affected = fused.updateTile(2, 2, tileData);
const inc = await fused.compute();
console.log('\n== 增量重算 ==');
console.log(`  修改 tile(2,2) -> 受影响输出 tile ${affected.length} 个, 本次重算 ${inc.stats.tilesComputed - fusedRes.stats.tilesComputed} 个`);
const input2 = Float32Array.from(input);
for (let y = 0; y < TILE; y++) {
  for (let x = 0; x < TILE; x++) {
    for (let c = 0; c < CHANNELS; c++) {
      input2[((2 * TILE + y) * W + 2 * TILE + x) * CHANNELS + c] = tileData[(y * TILE + x) * CHANNELS + c];
    }
  }
}
const fresh = new Pipeline(
  { dir: join(dir, 'fresh'), width: W, height: H, tileSize: TILE, budgetBytes: minFused * 4, mode: 'fused', kernels },
  input2,
);
const freshRes = await fresh.compute();
console.log(`  增量结果 vs 全新全量 maxDiff = ${maxDiff(inc.image, freshRes.image).toExponential(2)}`);

// 5) 预算不足拒绝
console.log('\n== 预算检查 ==');
try {
  new Pipeline(
    { dir: join(dir, 'poor'), width: W, height: H, tileSize: TILE, budgetBytes: minFused - 1, mode: 'fused', kernels },
    input,
  );
  console.log('  异常: 未被拒绝!');
} catch (err) {
  if (err instanceof BudgetError) console.log(`  预算 ${minFused - 1}B 被拒绝: ${err.message}`);
}

// 6) 根索引与预览图
console.log('\n== 发布产物 ==');
console.log(`  根索引: ${fused.indexPath}`);
console.log(`  内容: kernelVersion=${fused.readIndex().kernelVersion} tiles=${fused.readIndex().tiles}`);
writeFileSync(join(dir, 'out.ppm'), toPpm(inc.image, W, H));
console.log(`  预览: ${join(dir, 'out.ppm')}`);

fused.dispose();
step.dispose();
fresh.dispose();

function toPpm(image: Float32Array, w: number, h: number): Buffer {
  const header = Buffer.from(`P6\n${w} ${h}\n255\n`);
  const body = Buffer.alloc(w * h * 3);
  for (let i = 0; i < w * h; i++) {
    for (let c = 0; c < 3; c++) {
      const v = image[i * CHANNELS + c];
      body[i * 3 + c] = Math.max(0, Math.min(255, Math.round(v * 255)));
    }
  }
  return Buffer.concat([header, body]);
}
