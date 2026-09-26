import { createHash } from 'node:crypto';
import { mkdirSync, readdirSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import {
  clipRect,
  rectHeight,
  rectWidth,
  requiredInputRect,
  stageHalo,
  type KernelChain,
  type Rect,
} from './kernels.ts';
import {
  BYTES_PER_PIXEL,
  MemTracker,
  Metrics,
} from './metrics.ts';
import type { Raster } from './reference.ts';
import { makeSampler, runStage } from './stage.ts';
import {
  TileStore,
  TiledRaster,
  makeGrid,
  tileRect,
  tilesInRect,
  type TileGridSpec,
} from './tiles.ts';

export class CancelledError extends Error {
  constructor(message = 'pipeline cancelled') {
    super(message);
    this.name = 'CancelledError';
  }
}

export class InsufficientBudgetError extends Error {
  readonly requiredBytes: number;
  readonly budgetBytes: number;

  constructor(
    requiredBytes: number,
    budgetBytes: number,
  ) {
    super(
      `insufficient memory budget: need at least ${requiredBytes} B ` +
        `(working set incl. halo), got ${budgetBytes} B`,
    );
    this.name = 'InsufficientBudgetError';
    this.requiredBytes = requiredBytes;
    this.budgetBytes = budgetBytes;
  }
}

export type ExecutionMode = 'fused' | 'stepwise';

export interface BudgetBreakdown {
  /** Live working buffers for the worst tile (halo regions included). */
  readonly workingBytes: number;
  /** Minimum tile-cache bytes: one full halo input region + one output tile. */
  readonly cacheBytes: number;
  readonly totalBytes: number;
}

function maxTileBytes(grid: TileGridSpec): number {
  return (
    Math.min(grid.tileSize, grid.width) *
    Math.min(grid.tileSize, grid.height) *
    BYTES_PER_PIXEL
  );
}

function pixels(r: Rect): number {
  return rectWidth(r) * rectHeight(r);
}

/**
 * Exact minimum budget: the worst-case live working set while computing one
 * tile (halo regions included) plus enough cache to hold that tile's input
 * region. Anything smaller is rejected up front.
 */
export function minimumBudgetBytes(
  width: number,
  height: number,
  tileSize: number,
  chain: KernelChain,
  mode: ExecutionMode,
): BudgetBreakdown {
  const grid = makeGrid(width, height, tileSize);
  const tileBytes = maxTileBytes(grid);
  let working = 0;
  let cacheTiles = 1;

  for (let ty = 0; ty < grid.rows; ty++) {
    for (let tx = 0; tx < grid.cols; tx++) {
      const out = tileRect(grid, tx, ty);
      if (mode === 'fused') {
        const k = chain.stages.length;
        const need: Rect[] = new Array(k + 1);
        need[k] = out;
        for (let s = k; s >= 1; s--) {
          need[s - 1] = requiredInputRect(need[s], chain.stages[s - 1], width, height);
        }
        for (let s = 1; s <= k; s++) {
          const halo = stageHalo(chain.stages[s - 1]);
          const tmp =
            rectWidth(need[s]) * (rectHeight(need[s]) + halo.top + halo.bottom);
          working = Math.max(working, pixels(need[s - 1]) + tmp + pixels(need[s]));
        }
        cacheTiles = Math.max(cacheTiles, tilesInRect(grid, need[0]).length + 1);
      } else {
        for (const st of chain.stages) {
          const inRect = requiredInputRect(out, st, width, height);
          const halo = stageHalo(st);
          const tmp = rectWidth(out) * (rectHeight(out) + halo.top + halo.bottom);
          working = Math.max(working, pixels(inRect) + tmp + pixels(out));
          cacheTiles = Math.max(cacheTiles, tilesInRect(grid, inRect).length + 1);
        }
      }
    }
  }
  const workingBytes = working * BYTES_PER_PIXEL;
  const cacheBytes = cacheTiles * tileBytes;
  return { workingBytes, cacheBytes, totalBytes: workingBytes + cacheBytes };
}

/** Compute one output tile by fusing the whole chain with exact halo regions. */
export function computeFusedTile(
  src: TiledRaster,
  chain: KernelChain,
  out: Rect,
  width: number,
  height: number,
  metrics: Metrics,
  tracker: MemTracker,
): Float32Array {
  const k = chain.stages.length;
  const need: Rect[] = new Array(k + 1);
  need[k] = out;
  for (let s = k; s >= 1; s--) {
    need[s - 1] = requiredInputRect(need[s], chain.stages[s - 1], width, height);
  }
  let buf = src.readRegion(need[0], tracker);
  for (let s = 1; s <= k; s++) {
    const st = chain.stages[s - 1];
    const sampler = makeSampler(
      buf.data,
      buf.rect.x0,
      buf.rect.y0,
      rectWidth(buf.rect),
      rectHeight(buf.rect),
      width,
      height,
      st.boundaryH,
      st.boundaryV,
    );
    const next = runStage(sampler, st, need[s], metrics, tracker);
    tracker.release(buf.data.byteLength);
    buf = next;
  }
  return buf.data;
}

/** Compute one tile of one stage reading the previous stage's tiled raster. */
export function computeStepwiseTile(
  prev: TiledRaster,
  st: KernelChain['stages'][number],
  out: Rect,
  width: number,
  height: number,
  metrics: Metrics,
  tracker: MemTracker,
): Float32Array {
  const inRect = requiredInputRect(out, st, width, height);
  const region = prev.readRegion(inRect, tracker);
  try {
    const sampler = makeSampler(
      region.data,
      region.rect.x0,
      region.rect.y0,
      rectWidth(region.rect),
      rectHeight(region.rect),
      width,
      height,
      st.boundaryH,
      st.boundaryV,
    );
    return runStage(sampler, st, out, metrics, tracker).data;
  } finally {
    tracker.release(region.data.byteLength);
  }
}

export interface PipelineHooks {
  /** Called after each output tile; may abort via the run's AbortSignal. */
  onTileComputed?: (key: string, computed: number, total: number) => void;
  /** Test hook: inject a one-time failure when computing this tile. */
  failOnceOnTile?: string;
  /** Test hook: invoked between a tile failure and its retry. */
  onBeforeTileRetry?: (key: string) => void;
}

export interface RunOptions {
  readonly source: Raster;
  readonly chain: KernelChain;
  readonly tileSize: number;
  readonly mode: ExecutionMode;
  readonly budgetBytes: number;
  readonly workDir: string;
  readonly signal?: AbortSignal;
  readonly hooks?: PipelineHooks;
  readonly maxRetries?: number;
}

export interface RunResult {
  readonly indexPath: string;
  readonly version: number;
  readonly ops: number;
  readonly peakMemoryBytes: number;
  readonly minBudgetBytes: number;
  readonly tilesComputed: number;
  readonly spillCount: number;
  readonly output: Raster;
}

class InjectedFault extends Error {
  constructor(key: string) {
    super(`injected fault on tile ${key}`);
    this.name = 'InjectedFault';
  }
}

function sha256(buf: Float32Array): string {
  return createHash('sha256')
    .update(Buffer.from(buf.buffer, buf.byteOffset, buf.byteLength))
    .digest('hex');
}

/**
 * Run the full pipeline over a source raster.
 *
 * Guarantees:
 *  - rejects up front when `budgetBytes` is below the exact minimum halo
 *    working set (InsufficientBudgetError);
 *  - the kernel chain is snapshotted: retries after a failure never pick up
 *    a hot-swapped chain mid-run;
 *  - on cancellation or failure every cache/spill/temp file is released and
 *    no index is published;
 *  - the root index is published atomically (temp file + rename) only after
 *    every output tile is durably written.
 */
export function runPipeline(opts: RunOptions): RunResult {
  const { width, height } = opts.source;
  const chain = opts.chain; // immutable snapshot for the whole run
  const budget = minimumBudgetBytes(width, height, opts.tileSize, chain, opts.mode);
  if (opts.budgetBytes < budget.totalBytes) {
    throw new InsufficientBudgetError(budget.totalBytes, opts.budgetBytes);
  }

  const metrics = new Metrics();
  const tracker = new MemTracker(opts.budgetBytes);
  const spillDir = join(opts.workDir, `.spill-${process.pid}-${Math.random().toString(36).slice(2)}`);
  const store = new TileStore(spillDir, opts.budgetBytes - budget.workingBytes, tracker);
  const tilesDir = join(opts.workDir, 'tiles');
  const writtenFiles: string[] = [];
  let indexTmp: string | null = null;
  const failedOnce = new Set<string>();
  const maxRetries = opts.maxRetries ?? 2;
  const grid = makeGrid(width, height, opts.tileSize);
  let tilesComputed = 0;
  const totalTiles = grid.cols * grid.rows;

  const checkCancel = (): void => {
    if (opts.signal?.aborted) throw new CancelledError();
  };

  const withRetry = <T>(key: string, fn: () => T): T => {
    let attempt = 0;
    for (;;) {
      try {
        if (opts.hooks?.failOnceOnTile === key && !failedOnce.has(key)) {
          failedOnce.add(key);
          throw new InjectedFault(key);
        }
        return fn();
      } catch (e) {
        if (e instanceof CancelledError || e instanceof InsufficientBudgetError) throw e;
        attempt++;
        if (attempt > maxRetries) throw e;
        opts.hooks?.onBeforeTileRetry?.(key);
        // retry re-runs fn against the same immutable chain snapshot
      }
    }
  };

  try {
    mkdirSync(tilesDir, { recursive: true });
    const srcTiled = new TiledRaster(store, 'src', width, height, opts.tileSize);
    srcTiled.writeFullRaster(width, height, opts.source.data);

    let outTiled: TiledRaster;
    if (opts.mode === 'fused') {
      outTiled = new TiledRaster(store, 'out', width, height, opts.tileSize);
      for (let ty = 0; ty < grid.rows; ty++) {
        for (let tx = 0; tx < grid.cols; tx++) {
          checkCancel();
          const key = `${tx},${ty}`;
          const data = withRetry(key, () =>
            computeFusedTile(
              srcTiled,
              chain,
              tileRect(grid, tx, ty),
              width,
              height,
              metrics,
              tracker,
            ),
          );
          outTiled.writeTile(tx, ty, data);
          tracker.release(data.byteLength); // cache now accounts for it
          tilesComputed++;
          opts.hooks?.onTileComputed?.(key, tilesComputed, totalTiles);
        }
      }
    } else {
      let current = srcTiled;
      chain.stages.forEach((st, si) => {
        const next = new TiledRaster(store, `stage${si}`, width, height, opts.tileSize);
        for (let ty = 0; ty < grid.rows; ty++) {
          for (let tx = 0; tx < grid.cols; tx++) {
            checkCancel();
            const key = `${si}:${tx},${ty}`;
            const data = withRetry(key, () =>
              computeStepwiseTile(
                current,
                st,
                tileRect(grid, tx, ty),
                width,
                height,
                metrics,
                tracker,
              ),
            );
            next.writeTile(tx, ty, data);
            tracker.release(data.byteLength);
            if (si === chain.stages.length - 1) {
              tilesComputed++;
              opts.hooks?.onTileComputed?.(`${tx},${ty}`, tilesComputed, totalTiles);
            }
          }
        }
        if (si > 0) store.removeNamespace(`stage${si - 1}:`);
        current = next;
      });
      outTiled = current;
    }

    // Persist every output tile before publishing the index.
    const tileEntries: unknown[] = [];
    for (const key of outTiled.keys()) {
      const { rect, data } = store.get(key);
      const [, coord] = key.split(':');
      const file = `tiles/v${chain.version}-${coord.replace(',', '_')}.f32`;
      const abs = join(opts.workDir, file);
      writeFileSync(abs, Buffer.from(data.buffer, data.byteOffset, data.byteLength));
      writtenFiles.push(abs);
      tileEntries.push({ key: coord, rect, file, sha256: sha256(data) });
    }

    const index = {
      version: chain.version,
      width,
      height,
      tileSize: opts.tileSize,
      mode: opts.mode,
      stages: chain.stages.map((st) => ({
        horizontal: { taps: [...st.horizontal.taps], anchor: st.horizontal.anchor },
        vertical: { taps: [...st.vertical.taps], anchor: st.vertical.anchor },
        boundaryH: st.boundaryH,
        boundaryV: st.boundaryV,
      })),
      tiles: tileEntries,
      stats: {
        ops: metrics.ops,
        peakMemoryBytes: tracker.peak,
        minBudgetBytes: budget.totalBytes,
      },
    };
    // Atomic publish: write temp file, then rename over the destination.
    indexTmp = join(opts.workDir, `.index.json.${process.pid}.tmp`);
    writeFileSync(indexTmp, JSON.stringify(index, null, 2));
    renameSync(indexTmp, join(opts.workDir, 'index.json'));
    indexTmp = null;

    const output = outTiled.toRaster();
    return {
      indexPath: join(opts.workDir, 'index.json'),
      version: chain.version,
      ops: metrics.ops,
      peakMemoryBytes: tracker.peak,
      minBudgetBytes: budget.totalBytes,
      tilesComputed,
      spillCount: store.spillCount,
      output,
    };
  } catch (e) {
    // Failure/cancellation: release every file this run created.
    for (const f of writtenFiles) rmSync(f, { force: true });
    if (indexTmp) rmSync(indexTmp, { force: true });
    throw e;
  } finally {
    store.dispose(); // releases cache and deletes all spill files
  }
}

/** Remove leftover temp artifacts of a work dir (defensive cleanup helper). */
export function cleanWorkDir(workDir: string): void {
  try {
    for (const f of readdirSync(workDir)) {
      if (f.startsWith('.spill-') || f.startsWith('.index.json.')) {
        rmSync(join(workDir, f), { recursive: true, force: true });
      }
    }
  } catch {
    // best effort
  }
}

export { clipRect };
