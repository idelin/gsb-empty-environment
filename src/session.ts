import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  chainHalo,
  expandRect,
  makeChain,
  rectsIntersect,
  type KernelChain,
  type StageSpec,
} from './kernels.ts';
import { CHANNELS, MemTracker, Metrics } from './metrics.ts';
import {
  InsufficientBudgetError,
  computeFusedTile,
  minimumBudgetBytes,
  runPipeline,
  type ExecutionMode,
  type PipelineHooks,
  type RunResult,
} from './pipeline.ts';
import type { Raster } from './reference.ts';
import { TileStore, TiledRaster, makeGrid, tileRect, tileKey } from './tiles.ts';

export interface SessionOptions {
  readonly source: Raster;
  readonly tileSize: number;
  readonly budgetBytes: number;
  readonly mode?: ExecutionMode;
}

export interface RecomputeResult {
  readonly affected: string[];
  readonly ops: number;
  readonly peakMemoryBytes: number;
  readonly version: number;
}

/**
 * Stateful convolver session on top of a mutable source raster.
 *
 *  - Kernel hot-swap bumps the chain version; tiles computed under older
 *    versions become stale and are recomputed locally on demand.
 *  - Editing a source tile invalidates only the output tiles inside the
 *    full chain halo (for a two-stage chain this is exactly the two-layer
 *    halo propagation neighbourhood).
 *  - Every recompute snapshots the chain: a retry after an injected failure
 *    keeps using the snapshot even if the chain was swapped in between.
 */
export class ConvolverSession {
  private chain: KernelChain;
  private readonly mode: ExecutionMode;
  private readonly grid;
  private output: Raster | null = null;
  private outputVersion = -1;
  private readonly tileVersions = new Map<string, number>();
  private lastRun: RunResult | null = null;
  private readonly opts: SessionOptions;

  constructor(
    opts: SessionOptions,
    stages: readonly StageSpec[],
  ) {
    this.opts = opts;
    this.chain = makeChain(1, stages);
    this.mode = opts.mode ?? 'fused';
    this.grid = makeGrid(opts.source.width, opts.source.height, opts.tileSize);
  }

  get currentChain(): KernelChain {
    return this.chain;
  }

  get currentVersion(): number {
    return this.chain.version;
  }

  /** Hot-swap kernels. Existing tiles keep their old version until recomputed. */
  swapKernels(stages: readonly StageSpec[]): KernelChain {
    this.chain = makeChain(this.chain.version + 1, stages);
    return this.chain;
  }

  /** Full recompute under the current chain snapshot. */
  computeAll(workDir: string, hooks?: PipelineHooks, signal?: AbortSignal): RunResult {
    const result = runPipeline({
      source: this.opts.source,
      chain: this.chain,
      tileSize: this.opts.tileSize,
      mode: this.mode,
      budgetBytes: this.opts.budgetBytes,
      workDir,
      hooks,
      signal,
    });
    this.output = result.output;
    this.outputVersion = result.version;
    for (let ty = 0; ty < this.grid.rows; ty++) {
      for (let tx = 0; tx < this.grid.cols; tx++) {
        this.tileVersions.set(tileKey(tx, ty), result.version);
      }
    }
    this.lastRun = result;
    return result;
  }

  /** Overwrite one source tile (RGBA float32, tile-sized). */
  modifySourceTile(tx: number, ty: number, data: Float32Array): void {
    const r = tileRect(this.grid, tx, ty);
    const w = r.x1 - r.x0;
    const h = r.y1 - r.y0;
    if (data.length !== w * h * CHANNELS) throw new Error('modifySourceTile: bad size');
    const src = this.opts.source.data;
    for (let y = 0; y < h; y++) {
      const off = ((r.y0 + y) * this.opts.source.width + r.x0) * CHANNELS;
      src.set(data.subarray(y * w * CHANNELS, (y + 1) * w * CHANNELS), off);
    }
  }

  /**
   * Output tiles whose chain-halo neighbourhood touches the given source
   * tile — the exact set a local edit can influence through every stage.
   */
  affectedTiles(tx: number, ty: number): string[] {
    const halo = chainHalo(this.chain);
    const influence = expandRect(tileRect(this.grid, tx, ty), halo);
    const keys: string[] = [];
    for (let oy = 0; oy < this.grid.rows; oy++) {
      for (let ox = 0; ox < this.grid.cols; ox++) {
        if (rectsIntersect(tileRect(this.grid, ox, oy), influence)) {
          keys.push(tileKey(ox, oy));
        }
      }
    }
    return keys;
  }

  /**
   * Recompute only the given output tiles against the current source,
   * patching them into the last output. Uses a chain snapshot so a retry
   * after failure cannot mix kernel versions.
   */
  recomputeTiles(keys: readonly string[], hooks?: PipelineHooks): RecomputeResult {
    if (!this.output) throw new Error('recomputeTiles: run computeAll first');
    const snapshot = this.chain;
    const { width, height } = this.opts.source;
    const budget = minimumBudgetBytes(width, height, this.opts.tileSize, snapshot, 'fused');
    if (this.opts.budgetBytes < budget.totalBytes) {
      throw new InsufficientBudgetError(budget.totalBytes, this.opts.budgetBytes);
    }
    const metrics = new Metrics();
    const tracker = new MemTracker(this.opts.budgetBytes);
    const spillDir = mkdtempSync(join(tmpdir(), 'tileconv-recompute-'));
    const store = new TileStore(spillDir, this.opts.budgetBytes - budget.workingBytes, tracker);
    const failedOnce = new Set<string>();
    try {
      const srcTiled = new TiledRaster(store, 'src', width, height, this.opts.tileSize);
      srcTiled.writeFullRaster(width, height, this.opts.source.data);
      const out = this.output.data;
      for (const key of keys) {
        const [tx, ty] = key.split(',').map(Number);
        const rect = tileRect(this.grid, tx, ty);
        let attempt = 0;
        for (;;) {
          try {
            if (hooks?.failOnceOnTile === key && !failedOnce.has(key)) {
              failedOnce.add(key);
              throw new Error(`injected fault on tile ${key}`);
            }
            const data = computeFusedTile(srcTiled, snapshot, rect, width, height, metrics, tracker);
            const w = rect.x1 - rect.x0;
            const h = rect.y1 - rect.y0;
            for (let y = 0; y < h; y++) {
              const off = ((rect.y0 + y) * width + rect.x0) * CHANNELS;
              out.set(data.subarray(y * w * CHANNELS, (y + 1) * w * CHANNELS), off);
            }
            tracker.release(data.byteLength);
            this.tileVersions.set(key, snapshot.version);
            break;
          } catch (e) {
            attempt++;
            if (attempt > 2) throw e;
            hooks?.onBeforeTileRetry?.(key);
          }
        }
      }
      this.outputVersion = snapshot.version;
      return {
        affected: [...keys],
        ops: metrics.ops,
        peakMemoryBytes: tracker.peak,
        version: snapshot.version,
      };
    } finally {
      store.dispose();
      rmSync(spillDir, { recursive: true, force: true });
    }
  }

  /** Recompute every tile whose stored version differs from the chain's. */
  recomputeStale(hooks?: PipelineHooks): RecomputeResult {
    const stale: string[] = [];
    for (let ty = 0; ty < this.grid.rows; ty++) {
      for (let tx = 0; tx < this.grid.cols; tx++) {
        const key = tileKey(tx, ty);
        if (this.tileVersions.get(key) !== this.chain.version) stale.push(key);
      }
    }
    if (stale.length === 0) {
      return { affected: [], ops: 0, peakMemoryBytes: 0, version: this.chain.version };
    }
    return this.recomputeTiles(stale, hooks);
  }

  getOutput(): Raster {
    if (!this.output) throw new Error('no output yet');
    return this.output;
  }

  tileVersion(key: string): number | undefined {
    return this.tileVersions.get(key);
  }
}
