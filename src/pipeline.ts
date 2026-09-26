import { mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { CHANNELS } from './types.ts';
import type { Halo, Region, SeparableKernel } from './types.ts';
import { chainHalo } from './kernel.ts';
import { ArraySource, Raster } from './raster.ts';
import { Stats } from './stats.ts';
import type { StatsSnapshot } from './stats.ts';
import { TileStore } from './store.ts';
import { minBudgetBytes, minScratchBytes, runFused, runStepwise } from './convolve.ts';
import type { ExecutionMode } from './convolve.ts';

export class BudgetError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'BudgetError';
  }
}

export class CancelError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CancelError';
  }
}

export class VersionConflictError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'VersionConflictError';
  }
}

export interface PipelineOptions {
  dir: string;
  width: number;
  height: number;
  tileSize: number;
  budgetBytes: number;
  mode: ExecutionMode;
  kernels: SeparableKernel[];
}

export interface ComputeOptions {
  signal?: AbortSignal;
  onTileComputed?: (done: number, total: number) => void;
}

export interface ComputeResult {
  image: Float32Array;
  stats: StatsSnapshot;
}

export class Pipeline {
  readonly opts: PipelineOptions;
  readonly stats = new Stats();
  private kernels: SeparableKernel[];
  private kernelVersion = 0;
  private input: Float32Array;
  private store: TileStore;
  private output: Raster;
  private tileVersion = new Map<string, number>();
  private dirty = new Set<string>();
  private runSeq = 0;

  constructor(opts: PipelineOptions, input: Float32Array) {
    if (opts.kernels.length < 1) throw new Error('至少需要一个可分离核');
    if (input.length !== opts.width * opts.height * CHANNELS) {
      throw new Error('输入像素长度与尺寸不符');
    }
    this.opts = opts;
    this.kernels = opts.kernels;
    this.input = Float32Array.from(input);
    this.checkBudget(this.kernels);
    mkdirSync(opts.dir, { recursive: true });
    this.store = new TileStore(
      join(opts.dir, 'spill'),
      opts.budgetBytes,
      opts.tileSize * opts.tileSize * CHANNELS * 4,
      this.stats,
    );
    this.output = new Raster('out', this.store, opts.width, opts.height, opts.tileSize);
    this.markAllDirty();
  }

  get version(): number {
    return this.kernelVersion;
  }

  get indexPath(): string {
    return join(this.opts.dir, 'index.json');
  }

  get spillDir(): string {
    return this.store.dir;
  }

  // 融合模式所需总 halo (链式求和)
  requiredHalo(): Halo {
    return chainHalo(this.kernels);
  }

  minBudgetBytes(): number {
    return minBudgetBytes(this.opts.width, this.opts.height, this.opts.tileSize, this.kernels, this.opts.mode);
  }

  private checkBudget(kernels: SeparableKernel[]): void {
    const need = minBudgetBytes(this.opts.width, this.opts.height, this.opts.tileSize, kernels, this.opts.mode);
    if (this.opts.budgetBytes < need) {
      const scratch = minScratchBytes(this.opts.width, this.opts.height, this.opts.tileSize, kernels, this.opts.mode);
      throw new BudgetError(
        `预算不足: 最小需要 ${need} 字节 (halo 窗口 ${scratch} + tile 缓存 ${need - scratch}), 当前预算 ${this.opts.budgetBytes} 字节`,
      );
    }
  }

  private markAllDirty(): void {
    this.dirty.clear();
    for (let ty = 0; ty < this.output.tilesY; ty++) {
      for (let tx = 0; tx < this.output.tilesX; tx++) {
        this.dirty.add(this.output.key(tx, ty));
      }
    }
  }

  // 核热替换: 版本号递增, 全部输出标记待算 (惰性局部重算)
  swapKernels(kernels: SeparableKernel[]): void {
    if (kernels.length < 1) throw new Error('至少需要一个可分离核');
    this.checkBudget(kernels);
    this.kernels = kernels;
    this.kernelVersion += 1;
    this.markAllDirty();
  }

  setInput(input: Float32Array): void {
    if (input.length !== this.input.length) throw new Error('输入像素长度与尺寸不符');
    this.input = Float32Array.from(input);
    this.markAllDirty();
  }

  // 修改单个输入 tile, 返回经链式 halo 传播后受影响的输出 tile key 列表
  updateTile(tx: number, ty: number, data: Float32Array): string[] {
    const region = this.output.tileRegion(tx, ty);
    if (data.length !== region.w * region.h * CHANNELS) {
      throw new Error(`updateTile 数据长度不符: 期望 ${region.w * region.h * CHANNELS}, 实际 ${data.length}`);
    }
    for (let y = 0; y < region.h; y++) {
      const srcOff = y * region.w * CHANNELS;
      const dstOff = ((region.y0 + y) * this.opts.width + region.x0) * CHANNELS;
      this.input.set(data.subarray(srcOff, srcOff + region.w * CHANNELS), dstOff);
    }
    const affected = this.affectedTiles(region);
    for (const key of affected) this.dirty.add(key);
    return affected;
  }

  // 脏区域经各级 halo 逐级外扩 (= 链式总 halo); wrap 策略下触及一侧边缘时对侧边缘也受影响
  affectedTiles(region: Region): string[] {
    const { width: W, height: H } = this.opts;
    const halo = chainHalo(this.kernels);
    const bands: Region[] = [
      {
        x0: region.x0 - halo.left,
        y0: region.y0 - halo.top,
        w: region.w + halo.left + halo.right,
        h: region.h + halo.top + halo.bottom,
      },
    ];
    const hx = halo.left + halo.right;
    const hy = halo.top + halo.bottom;
    if (hx > 0 && this.kernels.some((k) => k.boundaryX === 'wrap')) {
      if (region.x0 < hx) bands.push({ x0: W - hx, y0: 0, w: hx, h: H });
      if (region.x0 + region.w > W - hx) bands.push({ x0: 0, y0: 0, w: hx, h: H });
    }
    if (hy > 0 && this.kernels.some((k) => k.boundaryY === 'wrap')) {
      if (region.y0 < hy) bands.push({ x0: 0, y0: H - hy, w: W, h: hy });
      if (region.y0 + region.h > H - hy) bands.push({ x0: 0, y0: 0, w: W, h: hy });
    }
    const keys: string[] = [];
    for (let ty = 0; ty < this.output.tilesY; ty++) {
      for (let tx = 0; tx < this.output.tilesX; tx++) {
        const tr = this.output.tileRegion(tx, ty);
        if (bands.some((b) => intersects(b, tr))) keys.push(this.output.key(tx, ty));
      }
    }
    return keys;
  }

  async compute(options: ComputeOptions = {}): Promise<ComputeResult> {
    const version = this.kernelVersion;
    this.checkBudget(this.kernels);
    this.store.setScratchReserve(
      minScratchBytes(this.opts.width, this.opts.height, this.opts.tileSize, this.kernels, this.opts.mode),
    );
    const src = new ArraySource(this.input, this.opts.width, this.opts.height);
    const stale = (tx: number, ty: number): boolean => {
      const key = this.output.key(tx, ty);
      return this.dirty.has(key) || this.tileVersion.get(key) !== version;
    };
    let total = 0;
    for (let ty = 0; ty < this.output.tilesY; ty++) {
      for (let tx = 0; tx < this.output.tilesX; tx++) {
        if (stale(tx, ty)) total += 1;
      }
    }
    if (this.opts.mode === 'stepwise' && total > 0) {
      total = this.output.tilesX * this.output.tilesY;
    }
    let done = 0;
    const checkAlive = (): void => {
      if (options.signal?.aborted) {
        throw new CancelError('任务已取消: 已释放缓存与临时文件');
      }
      if (this.kernelVersion !== version) {
        throw new VersionConflictError(
          `计算期间核被热替换 (v${version} -> v${this.kernelVersion}), 部分结果已作废, 重试将统一使用 v${this.kernelVersion}`,
        );
      }
    };
    const runId = ++this.runSeq;
    try {
      if (this.opts.mode === 'fused') {
        runFused(
          src,
          this.output,
          this.kernels,
          this.stats,
          {
            shouldAbort: checkAlive,
            onTile: (tx, ty) => {
              const key = this.output.key(tx, ty);
              this.tileVersion.set(key, version);
              this.dirty.delete(key);
              done += 1;
              options.onTileComputed?.(done, total);
            },
          },
          (tx, ty) => stale(tx, ty),
        );
      } else if (total > 0) {
        // 分步模式: 中间图物化到可溢写 store, 全量重算
        const image = runStepwise(
          src,
          this.opts.width,
          this.opts.height,
          this.opts.tileSize,
          this.kernels,
          this.store,
          this.stats,
          `run${runId}`,
          {
            shouldAbort: checkAlive,
            onTile: () => {
              done += 1;
              options.onTileComputed?.(done, total);
            },
          },
        );
        this.output.writeRegion(0, 0, this.opts.width, this.opts.height, image);
        for (let ty = 0; ty < this.output.tilesY; ty++) {
          for (let tx = 0; tx < this.output.tilesX; tx++) {
            const key = this.output.key(tx, ty);
            this.tileVersion.set(key, version);
            this.dirty.delete(key);
          }
        }
      }
      const image = this.output.readRegion(0, 0, this.opts.width, this.opts.height);
      this.publishIndex();
      return { image, stats: this.stats.snapshot() };
    } catch (err) {
      // 取消 / 版本冲突 / 失败: 释放缓存与临时文件, 全部标记待算, 重试不会混用新旧核版本
      this.store.releaseAll();
      this.tileVersion.clear();
      this.markAllDirty();
      throw err;
    }
  }

  // 原子发布根索引: 先写临时文件再 rename
  private publishIndex(): void {
    const index = {
      kernelVersion: this.kernelVersion,
      kernels: this.kernels.map((k) => k.id),
      width: this.opts.width,
      height: this.opts.height,
      tileSize: this.opts.tileSize,
      mode: this.opts.mode,
      tiles: this.output.tilesX * this.output.tilesY,
      stats: this.stats.snapshot(),
      publishedAt: new Date().toISOString(),
    };
    const tmp = join(this.opts.dir, 'index.json.tmp');
    writeFileSync(tmp, JSON.stringify(index, null, 2));
    renameSync(tmp, this.indexPath);
  }

  readIndex(): Record<string, unknown> {
    return JSON.parse(readFileSync(this.indexPath, 'utf8')) as Record<string, unknown>;
  }

  dispose(): void {
    this.store.dispose();
  }
}

function intersects(a: Region, b: Region): boolean {
  return a.x0 < b.x0 + b.w && b.x0 < a.x0 + a.w && a.y0 < b.y0 + b.h && b.y0 < a.y0 + a.h;
}
