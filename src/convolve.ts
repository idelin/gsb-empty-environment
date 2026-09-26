import { CHANNELS } from './types.ts';
import type { BoundaryPolicy, Region, SeparableKernel } from './types.ts';
import { mapIndex, mappedInterval } from './boundary.ts';
import { chainHalo, stageHalo } from './kernel.ts';
import { Raster } from './raster.ts';
import type { PixelSource } from './raster.ts';
import type { Stats } from './stats.ts';
import type { TileStore } from './store.ts';

export type ExecutionMode = 'fused' | 'stepwise';

export interface RunHooks {
  shouldAbort?: () => void;
  onTile?: (tx: number, ty: number) => void;
}

// 水平 1D 卷积: 从窗口 (srcX0, srcY0, srcW, srcH) 读取, 输出 x∈[outX0, outX0+outW), 行不变。
// 越界采样经 boundary 映射回图像域, 调用方须保证映射结果落在窗口内 (由 planStage 保证)。
export function passHWindow(
  src: Float32Array,
  srcW: number,
  srcH: number,
  srcX0: number,
  srcY0: number,
  outX0: number,
  outW: number,
  imgW: number,
  k: Float64Array,
  anchor: number,
  boundary: BoundaryPolicy,
  stats?: Stats,
): Float32Array {
  const out = new Float32Array(outW * srcH * CHANNELS);
  const kl = k.length;
  for (let y = 0; y < srcH; y++) {
    for (let x = 0; x < outW; x++) {
      const gx = outX0 + x;
      let r = 0;
      let g = 0;
      let b = 0;
      let a = 0;
      let performed = 0;
      for (let j = 0; j < kl; j++) {
        const m = mapIndex(gx + j - anchor, imgW, boundary);
        if (m === null) continue;
        const sx = m - srcX0;
        if (sx < 0 || sx >= srcW) {
          throw new RangeError(`halo 推导错误: x=${m} 不在窗口 [${srcX0}, ${srcX0 + srcW})`);
        }
        const off = (y * srcW + sx) * CHANNELS;
        const wgt = k[j];
        r += wgt * src[off];
        g += wgt * src[off + 1];
        b += wgt * src[off + 2];
        a += wgt * src[off + 3];
        performed++;
      }
      const o = (y * outW + x) * CHANNELS;
      out[o] = r;
      out[o + 1] = g;
      out[o + 2] = b;
      out[o + 3] = a;
      stats?.addMacs(performed * CHANNELS);
    }
  }
  return out;
}

// 垂直 1D 卷积: 输出 y∈[outY0, outY0+outH), 列不变
export function passVWindow(
  src: Float32Array,
  srcW: number,
  srcH: number,
  srcX0: number,
  srcY0: number,
  outY0: number,
  outH: number,
  imgH: number,
  k: Float64Array,
  anchor: number,
  boundary: BoundaryPolicy,
  stats?: Stats,
): Float32Array {
  const out = new Float32Array(srcW * outH * CHANNELS);
  const kl = k.length;
  const rowLen = srcW * CHANNELS;
  for (let y = 0; y < outH; y++) {
    const gy = outY0 + y;
    const outRow = y * rowLen;
    let performed = 0;
    for (let j = 0; j < kl; j++) {
      const m = mapIndex(gy + j - anchor, imgH, boundary);
      if (m === null) continue;
      const sy = m - srcY0;
      if (sy < 0 || sy >= srcH) {
        throw new RangeError(`halo 推导错误: y=${m} 不在窗口 [${srcY0}, ${srcY0 + srcH})`);
      }
      const srcRow = sy * rowLen;
      const wgt = k[j];
      for (let i = 0; i < rowLen; i++) out[outRow + i] += wgt * src[srcRow + i];
      performed++;
    }
    stats?.addMacs(performed * rowLen);
  }
  return out;
}

// 未分块逐像素参考实现 (oracle)
export function convolveReference(
  src: Float32Array,
  width: number,
  height: number,
  kernels: readonly SeparableKernel[],
  stats?: Stats,
): Float32Array {
  let cur = src;
  for (const k of kernels) {
    const tmp = passHWindow(cur, width, height, 0, 0, 0, width, width, k.hx, k.anchorX, k.boundaryX, stats);
    cur = passVWindow(tmp, width, height, 0, 0, 0, height, height, k.hy, k.anchorY, k.boundaryY, stats);
  }
  return cur;
}

export interface StagePlan {
  input: Region;
  midY0: number;
  midY1: number;
}

// 单级反向区域推导: 为得到 out 区域的输出, 需要输入图像的哪个区域。
// 边界策略只作用于图像真实边缘 (经 mappedInterval 映射), tile 边界绝不当作图像边界。
export function planStage(out: Region, imgW: number, imgH: number, k: SeparableKernel): StagePlan {
  const halo = stageHalo(k);
  const [my0, my1] = mappedInterval(out.y0 - halo.top, out.y0 + out.h + halo.bottom, imgH, k.boundaryY);
  const [mx0, mx1] = mappedInterval(out.x0 - halo.left, out.x0 + out.w + halo.right, imgW, k.boundaryX);
  return { input: { x0: mx0, y0: my0, w: mx1 - mx0, h: my1 - my0 }, midY0: my0, midY1: my1 };
}

export interface ChainPlan {
  regions: Region[]; // regions[i] = 第 i 级输入窗口, regions[length] = 输出区域
  mids: Array<{ y0: number; y1: number }>;
}

export function planChain(out: Region, imgW: number, imgH: number, kernels: readonly SeparableKernel[]): ChainPlan {
  const regions: Region[] = new Array(kernels.length + 1);
  const mids: Array<{ y0: number; y1: number }> = new Array(kernels.length);
  regions[kernels.length] = out;
  for (let i = kernels.length - 1; i >= 0; i--) {
    const plan = planStage(regions[i + 1], imgW, imgH, kernels[i]);
    regions[i] = plan.input;
    mids[i] = { y0: plan.midY0, y1: plan.midY1 };
  }
  return { regions, mids };
}

// 融合执行: 单个输出 tile 一次取齐链式 halo 窗口, 逐级收缩, 不物化中间图
export function computeTileFused(
  src: PixelSource,
  imgW: number,
  imgH: number,
  kernels: readonly SeparableKernel[],
  out: Region,
  stats?: Stats,
  store?: TileStore,
): Float32Array {
  const { regions, mids } = planChain(out, imgW, imgH, kernels);
  let maxPixels = 0;
  for (const r of regions) maxPixels = Math.max(maxPixels, r.w * r.h);
  for (let i = 0; i < kernels.length; i++) {
    maxPixels = Math.max(maxPixels, regions[i + 1].w * (mids[i].y1 - mids[i].y0));
  }
  const scratch = maxPixels * CHANNELS * 4 * 2;
  store?.reserveScratch(scratch);
  try {
    let win = src.readRegion(regions[0].x0, regions[0].y0, regions[0].w, regions[0].h);
    let reg = regions[0];
    for (let i = 0; i < kernels.length; i++) {
      const k = kernels[i];
      const next = regions[i + 1];
      const tmp = passHWindow(win, reg.w, reg.h, reg.x0, reg.y0, next.x0, next.w, imgW, k.hx, k.anchorX, k.boundaryX, stats);
      win = passVWindow(tmp, next.w, mids[i].y1 - mids[i].y0, next.x0, mids[i].y0, next.y0, next.h, imgH, k.hy, k.anchorY, k.boundaryY, stats);
      reg = next;
    }
    return win;
  } finally {
    store?.releaseScratch(scratch);
  }
}

export function runFused(
  src: PixelSource,
  out: Raster,
  kernels: readonly SeparableKernel[],
  stats: Stats,
  hooks?: RunHooks,
  filter?: (tx: number, ty: number) => boolean,
): void {
  for (let ty = 0; ty < out.tilesY; ty++) {
    for (let tx = 0; tx < out.tilesX; tx++) {
      hooks?.shouldAbort?.();
      if (filter && !filter(tx, ty)) continue;
      const region = out.tileRegion(tx, ty);
      const data = computeTileFused(src, out.width, out.height, kernels, region, stats, out.store);
      out.writeRegion(region.x0, region.y0, region.w, region.h, data);
      stats.tilesComputed += 1;
      hooks?.onTile?.(tx, ty);
    }
  }
}

// 分步执行: 每级的 H/V 中间图物化到可溢写 store, 每步只需自身 halo
export function runStepwise(
  src: PixelSource,
  imgW: number,
  imgH: number,
  tileSize: number,
  kernels: readonly SeparableKernel[],
  store: TileStore,
  stats: Stats,
  scopePrefix: string,
  hooks?: RunHooks,
): Float32Array {
  let current: PixelSource = src;
  let currentRaster: Raster | null = null;
  let stageIndex = 0;
  for (const k of kernels) {
    const halo = stageHalo(k);
    const mid = new Raster(`${scopePrefix}-s${stageIndex}h`, store, imgW, imgH, tileSize);
    const scratchH = Math.min(imgW, tileSize + halo.left + halo.right) * Math.min(tileSize, imgH) * CHANNELS * 4 * 2;
    store.reserveScratch(scratchH);
    try {
      for (let ty = 0; ty < mid.tilesY; ty++) {
        for (let tx = 0; tx < mid.tilesX; tx++) {
          hooks?.shouldAbort?.();
          const tile = mid.tileRegion(tx, ty);
          const [x0, x1] = mappedInterval(tile.x0 - halo.left, tile.x0 + tile.w + halo.right, imgW, k.boundaryX);
          const win = current.readRegion(x0, tile.y0, x1 - x0, tile.h);
          const out = passHWindow(win, x1 - x0, tile.h, x0, tile.y0, tile.x0, tile.w, imgW, k.hx, k.anchorX, k.boundaryX, stats);
          mid.writeRegion(tile.x0, tile.y0, tile.w, tile.h, out);
          stats.tilesComputed += 1;
          hooks?.onTile?.(tx, ty);
        }
      }
    } finally {
      store.releaseScratch(scratchH);
    }
    const outRaster = new Raster(`${scopePrefix}-s${stageIndex}v`, store, imgW, imgH, tileSize);
    const scratchV = Math.min(tileSize, imgW) * Math.min(imgH, tileSize + halo.top + halo.bottom) * CHANNELS * 4 * 2;
    store.reserveScratch(scratchV);
    try {
      for (let ty = 0; ty < outRaster.tilesY; ty++) {
        for (let tx = 0; tx < outRaster.tilesX; tx++) {
          hooks?.shouldAbort?.();
          const tile = outRaster.tileRegion(tx, ty);
          const [y0, y1] = mappedInterval(tile.y0 - halo.top, tile.y0 + tile.h + halo.bottom, imgH, k.boundaryY);
          const win = mid.readRegion(tile.x0, y0, tile.w, y1 - y0);
          const out = passVWindow(win, tile.w, y1 - y0, tile.x0, y0, tile.y0, tile.h, imgH, k.hy, k.anchorY, k.boundaryY, stats);
          outRaster.writeRegion(tile.x0, tile.y0, tile.w, tile.h, out);
          stats.tilesComputed += 1;
          hooks?.onTile?.(tx, ty);
        }
      }
    } finally {
      store.releaseScratch(scratchV);
    }
    mid.dispose();
    currentRaster?.dispose();
    current = outRaster;
    currentRaster = outRaster;
    stageIndex++;
  }
  const image = current.readRegion(0, 0, imgW, imgH);
  currentRaster?.dispose();
  return image;
}

// 最小 scratch 像素量: 融合 = tile + 链式总 halo; 分步 = 各级 halo 的最大值
export function minScratchPixels(
  imgW: number,
  imgH: number,
  tileSize: number,
  kernels: readonly SeparableKernel[],
  mode: ExecutionMode,
): number {
  const tw = Math.min(tileSize, imgW);
  const th = Math.min(tileSize, imgH);
  if (mode === 'fused') {
    const halo = chainHalo(kernels);
    return Math.min(imgW, tw + halo.left + halo.right) * Math.min(imgH, th + halo.top + halo.bottom);
  }
  let max = 0;
  for (const k of kernels) {
    const halo = stageHalo(k);
    max = Math.max(
      max,
      Math.min(imgW, tw + halo.left + halo.right) * th,
      tw * Math.min(imgH, th + halo.top + halo.bottom),
    );
  }
  return max;
}

export function minScratchBytes(
  imgW: number,
  imgH: number,
  tileSize: number,
  kernels: readonly SeparableKernel[],
  mode: ExecutionMode,
): number {
  return minScratchPixels(imgW, imgH, tileSize, kernels, mode) * CHANNELS * 4 * 2;
}

// 最小预算 = halo 窗口 scratch(输入+输出缓冲) + 至少 2 个 tile 的缓存
export function minBudgetBytes(
  imgW: number,
  imgH: number,
  tileSize: number,
  kernels: readonly SeparableKernel[],
  mode: ExecutionMode,
): number {
  const tileBytes = tileSize * tileSize * CHANNELS * 4;
  return minScratchBytes(imgW, imgH, tileSize, kernels, mode) + 2 * tileBytes;
}
