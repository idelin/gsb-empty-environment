/**
 * Runnable demo: builds a small RGBA test image from scratch (no Canvas, no
 * image libraries), runs a chained odd+even separable kernel pipeline in
 * fused and stepwise modes with a tight memory budget (forcing tile spills),
 * and prints compute cost, peak memory and an ASCII preview.
 *
 * Run: npm run example
 */
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { kernel, makeChain, stage } from '../src/kernels.ts';
import { minimumBudgetBytes, runPipeline } from '../src/pipeline.ts';
import { convolveReference, makeRaster } from '../src/reference.ts';

const W = 96;
const H = 64;
const TILE = 16;

function buildImage(): Float32Array {
  const data = new Float32Array(W * H * 4);
  for (let y = 0; y < H; y++) {
    for (let x = 0; x < W; x++) {
      const off = (y * W + x) * 4;
      const dx = x - W * 0.62;
      const dy = y - H * 0.45;
      const disc = Math.max(0, 1 - Math.sqrt(dx * dx + dy * dy) / 18);
      const stripes = ((x >> 3) + (y >> 3)) % 2 === 0 ? 0.15 : 0;
      data[off] = Math.min(1, x / W + disc * 0.6);
      data[off + 1] = Math.min(1, y / H + disc * 0.4 + stripes);
      data[off + 2] = Math.min(1, 0.3 + 0.4 * Math.sin(x * 0.3) * Math.cos(y * 0.2) + disc);
      data[off + 3] = 1;
    }
  }
  return data;
}

function asciiPreview(data: Float32Array, cols: number, rows: number): string {
  const ramp = ' .:-=+*#%@';
  let out = '';
  for (let ry = 0; ry < rows; ry++) {
    for (let rx = 0; rx < cols; rx++) {
      const x = Math.min(W - 1, Math.floor((rx * W) / cols));
      const y = Math.min(H - 1, Math.floor((ry * H) / rows));
      const off = (y * W + x) * 4;
      const lum = 0.2126 * data[off] + 0.7152 * data[off + 1] + 0.0722 * data[off + 2];
      out += ramp[Math.max(0, Math.min(9, Math.floor(lum * 10)))];
    }
    out += '\n';
  }
  return out;
}

const source = makeRaster(W, H, buildImage());

// Chained separable kernels: odd 3-tap blur, then even 4-tap unsharp mask
// with an off-centre anchor, per-stage boundary policies.
const chain = makeChain(1, [
  stage(kernel([1, 2, 1].map((v) => v / 4)), kernel([1, 2, 1].map((v) => v / 4)), 'mirror', 'mirror'),
  stage(kernel([-0.1, 1.25, -0.1, 0.05], 1), kernel([-0.1, 1.25, -0.1, 0.05], 1), 'clamp', 'reflect101'),
]);

const outDir = new URL('./.demo-out/', import.meta.url).pathname;
mkdirSync(outDir, { recursive: true });

for (const mode of ['fused', 'stepwise'] as const) {
  const min = minimumBudgetBytes(W, H, TILE, chain, mode);
  const result = runPipeline({
    source,
    chain,
    tileSize: TILE,
    mode,
    budgetBytes: min.totalBytes, // exact minimum budget -> forces spills
    workDir: join(outDir, mode),
  });
  const reference = convolveReference(source, chain);
  let maxDiff = 0;
  for (let i = 0; i < reference.data.length; i++) {
    maxDiff = Math.max(maxDiff, Math.abs(reference.data[i] - result.output.data[i]));
  }
  console.log(`--- ${mode} ---`);
  console.log(`  min budget:      ${min.totalBytes} B (working ${min.workingBytes} B + cache ${min.cacheBytes} B)`);
  console.log(`  ops (MACs):      ${result.ops}`);
  console.log(`  peak memory:     ${result.peakMemoryBytes} B`);
  console.log(`  tiles computed:  ${result.tilesComputed}`);
  console.log(`  tile spills:     ${result.spillCount}`);
  console.log(`  max |diff| vs untiled reference: ${maxDiff}`);
  console.log(`  root index:      ${result.indexPath}`);

  // Reject a too-small budget to demonstrate the guard.
  try {
    runPipeline({
      source,
      chain,
      tileSize: TILE,
      mode,
      budgetBytes: min.totalBytes - 1,
      workDir: join(outDir, `${mode}-poor`),
    });
    console.log('  budget guard:    FAILED (should have rejected)');
  } catch (e) {
    console.log(`  budget guard:    rejected ${(e as Error).message}`);
  }
}

const finalRun = runPipeline({
  source,
  chain,
  tileSize: TILE,
  mode: 'fused',
  budgetBytes: 1 << 26,
  workDir: join(outDir, 'final'),
});
writeFileSync(
  join(outDir, 'output.rgba'),
  Buffer.from(finalRun.output.data.buffer, finalRun.output.data.byteOffset, finalRun.output.data.byteLength),
);
console.log('\npreview (fused output, luminance):');
console.log(asciiPreview(finalRun.output.data, 64, 20));
console.log(`raw RGBA float32 written to ${join(outDir, 'output.rgba')}`);
