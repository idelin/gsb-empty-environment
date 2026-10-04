import type { PixelChange, PixelEdit, Plane } from './types.ts';
import { labelPasses } from './types.ts';
import { localOpPlane, localOpPoint, makeResolvedSampler } from './ops.ts';
import { forwardDeltas, reconstructReference, symmetricDeltas } from './reconstruct.ts';
import { TileCache, TileStore } from './tiles.ts';
import type { CompiledGraph, CompiledLocalStage, CompiledReconstructStage, CompiledStage } from './graph.ts';

export type StageStatus = 'ok' | 'fixed-point' | 'budget-exhausted' | 'idle';

export interface StageReport {
  name: string;
  kind: string;
  status: StageStatus;
  iterations?: number;
  changedPixels: number;
}

export interface RunReport {
  stages: StageReport[];
}

export interface PipelineOptions {
  labels?: Plane; // static label plane consulted by every stage's label mask
  tileSize?: number; // default 16
  maxCachedTiles?: number; // decoded-tile budget shared by all stores; default 64
}

/**
 * Incremental execution engine for a compiled operation graph.
 *
 * - Every plane (sources and stage outputs) lives in a TileStore backed by
 *   RLE cold storage; decoded tiles are bounded by a shared LRU cache.
 * - Local stages recompute only the dirty region (change set dilated by the
 *   stage's influence radius).
 * - Reconstruction stages compute the conservative affected region A = the
 *   symmetrized-SE-connected components of (oldMask ∪ newMask) that touch a
 *   changed pixel, then re-derive the fixed point inside A from the current
 *   marker. Propagation is therefore never truncated by the kernel radius,
 *   and removed support is revoked exactly.
 * - Publication is transactional: a stage's published output is replaced
 *   only when its fixed point completes within budget; on budget exhaustion
 *   the previously published version is kept and reported.
 */
export class Pipeline {
  readonly cache: TileCache;
  private readonly tileSize: number;
  private readonly stores = new Map<string, TileStore>();
  private readonly labels?: Plane;
  private readonly graph: CompiledGraph;
  private readonly width: number;
  private readonly height: number;
  lastReport: RunReport;

  constructor(graph: CompiledGraph, sources: Record<string, Plane>, opts: PipelineOptions = {}) {
    this.graph = graph;
    this.tileSize = opts.tileSize ?? 16;
    this.cache = new TileCache(opts.maxCachedTiles ?? 64);
    this.labels = opts.labels;
    const first = Object.values(sources)[0];
    if (!first) throw new Error('at least one source plane is required');
    this.width = first.width;
    this.height = first.height;
    for (const [name, plane] of Object.entries(sources)) {
      if (plane.width !== this.width || plane.height !== this.height)
        throw new Error(`source '${name}' has mismatched dimensions`);
      this.stores.set(name, TileStore.fromPlane(plane, this.cache, this.tileSize));
    }
    if (this.labels && (this.labels.width !== this.width || this.labels.height !== this.height))
      throw new Error('label plane has mismatched dimensions');
    for (const stage of graph.stages) {
      this.stores.set(stage.name, new TileStore(this.width, this.height, this.cache, this.tileSize));
    }
    this.lastReport = this.runFull();
  }

  private store(name: string): TileStore {
    const s = this.stores.get(name);
    if (!s) throw new Error(`unknown plane '${name}'`);
    return s;
  }

  private labelPassesAt(stage: CompiledStage, x: number, y: number): boolean {
    if (!this.labels) return true;
    return labelPasses(stage.labels, this.labels.data[y * this.width + x]);
  }

  /** Full (re)computation of every stage, in topological order. */
  private runFull(): RunReport {
    const stages: StageReport[] = [];
    for (const stage of this.graph.stages) {
      if (stage.kind === 'local') {
        const input = this.store(stage.input).toPlane();
        const out = localOpPlane(stage.op, input, stage.se, stage.boundary, {
          plane: this.labels,
          mask: stage.labels,
        });
        const store = this.store(stage.name);
        let changed = 0;
        for (let y = 0; y < this.height; y++)
          for (let x = 0; x < this.width; x++) {
            const v = out.data[y * this.width + x];
            if (store.get(x, y) !== v) changed++;
            store.set(x, y, v);
          }
        stages.push({ name: stage.name, kind: stage.kind, status: 'ok', changedPixels: changed });
      } else {
        stages.push(this.runFullReconstruct(stage));
      }
    }
    return { stages };
  }

  private runFullReconstruct(stage: CompiledReconstructStage): StageReport {
    const marker = this.store(stage.marker).toPlane();
    const mask = this.store(stage.mask).toPlane();
    const { result, converged, iterations } = reconstructReference(
      marker,
      mask,
      stage.se,
      stage.boundary,
      stage.maxIterations,
    );
    if (!converged) {
      // Budget exhausted: keep the previously published output untouched.
      return { name: stage.name, kind: stage.kind, status: 'budget-exhausted', iterations, changedPixels: 0 };
    }
    const store = this.store(stage.name);
    let changed = 0;
    for (let y = 0; y < this.height; y++)
      for (let x = 0; x < this.width; x++) {
        const i = y * this.width + x;
        const v = this.labelPassesAt(stage, x, y) ? result.data[i] : (marker.data[i] > 0 ? 1 : 0);
        if (store.get(x, y) !== v) changed++;
        store.set(x, y, v);
      }
    return { name: stage.name, kind: stage.kind, status: 'fixed-point', iterations, changedPixels: changed };
  }

  /** Apply local edits to a source plane and incrementally maintain all stages. */
  applyEdits(sourceName: string, edits: PixelEdit[]): RunReport {
    const source = this.store(sourceName);
    const initial: PixelChange[] = [];
    for (const e of edits) {
      const oldValue = source.get(e.x, e.y);
      const newValue = e.value > 0 ? 1 : 0;
      if (oldValue !== newValue) {
        source.set(e.x, e.y, newValue);
        initial.push({ x: e.x, y: e.y, oldValue, newValue });
      }
    }
    const changesByPlane = new Map<string, PixelChange[]>();
    if (initial.length > 0) changesByPlane.set(sourceName, initial);

    const stages: StageReport[] = [];
    for (const stage of this.graph.stages) {
      let report: StageReport;
      let changes: PixelChange[];
      if (stage.kind === 'local') {
        const inputChanges = changesByPlane.get(stage.input) ?? [];
        [report, changes] = this.updateLocalStage(stage, inputChanges);
      } else {
        const markerChanges = changesByPlane.get(stage.marker) ?? [];
        const maskChanges = changesByPlane.get(stage.mask) ?? [];
        [report, changes] = this.updateReconstructStage(stage, markerChanges, maskChanges);
      }
      stages.push(report);
      if (changes.length > 0) changesByPlane.set(stage.name, changes);
    }
    this.lastReport = { stages };
    return this.lastReport;
  }

  private updateLocalStage(stage: CompiledLocalStage, inputChanges: PixelChange[]): [StageReport, PixelChange[]] {
    if (inputChanges.length === 0) {
      return [{ name: stage.name, kind: stage.kind, status: 'idle', changedPixels: 0 }, []];
    }
    const input = this.store(stage.input);
    const sample = makeResolvedSampler(
      (x, y) => input.get(x, y),
      this.width,
      this.height,
      stage.boundary,
    );
    const dirty = new Set<number>();
    for (const c of inputChanges) {
      for (let dy = -stage.ry; dy <= stage.ry; dy++)
        for (let dx = -stage.rx; dx <= stage.rx; dx++) {
          const x = c.x + dx;
          const y = c.y + dy;
          if (x >= 0 && y >= 0 && x < this.width && y < this.height) dirty.add(y * this.width + x);
        }
    }
    const out = this.store(stage.name);
    const changes: PixelChange[] = [];
    for (const idx of dirty) {
      const x = idx % this.width;
      const y = Math.floor(idx / this.width);
      const newValue = this.labelPassesAt(stage, x, y)
        ? localOpPoint(stage.op, sample, x, y, stage.se, this.width, this.height, stage.boundary)
        : input.get(x, y);
      const oldValue = out.get(x, y);
      if (oldValue !== newValue) {
        out.set(x, y, newValue);
        changes.push({ x, y, oldValue, newValue });
      }
    }
    return [{ name: stage.name, kind: stage.kind, status: 'ok', changedPixels: changes.length }, changes];
  }

  private updateReconstructStage(
    stage: CompiledReconstructStage,
    markerChanges: PixelChange[],
    maskChanges: PixelChange[],
  ): [StageReport, PixelChange[]] {
    const idle: [StageReport, PixelChange[]] = [
      { name: stage.name, kind: stage.kind, status: 'idle', changedPixels: 0 },
      [],
    ];
    if (markerChanges.length === 0 && maskChanges.length === 0) return idle;

    const marker = this.store(stage.marker);
    const mask = this.store(stage.mask);
    const out = this.store(stage.name);
    const changes: PixelChange[] = [];
    const writeOut = (x: number, y: number, newValue: number) => {
      const oldValue = out.get(x, y);
      if (oldValue !== newValue) {
        out.set(x, y, newValue);
        changes.push({ x, y, oldValue, newValue });
      }
    };

    // Label-masked pixels always pass the marker through directly.
    for (const c of markerChanges) {
      if (!this.labelPassesAt(stage, c.x, c.y)) writeOut(c.x, c.y, c.newValue);
    }

    // oldMask ∪ newMask at any pixel (old values only differ at changed pixels).
    const maskOldAt = new Map<number, number>();
    for (const c of maskChanges) maskOldAt.set(c.y * this.width + c.x, c.oldValue);
    const unionMask = (x: number, y: number): number => {
      const idx = y * this.width + x;
      const cur = mask.get(x, y);
      const old = maskOldAt.get(idx);
      return old === undefined ? cur : cur | old;
    };

    // Affected region A: symmetrized-SE-connected components of
    // (oldMask ∪ newMask) that touch a changed pixel. Never bounded by the
    // kernel radius, so long-range propagation and revocation stay exact.
    const region = new Set<number>();
    const sym = symmetricDeltas(stage.se);
    const stack: Array<readonly [number, number]> = [];
    const seed = (x: number, y: number) => {
      const idx = y * this.width + x;
      if (!region.has(idx) && unionMask(x, y) === 1) {
        region.add(idx);
        stack.push([x, y]);
      }
    };
    for (const c of markerChanges) seed(c.x, c.y);
    for (const c of maskChanges) seed(c.x, c.y);
    while (stack.length > 0) {
      const [x, y] = stack.pop() as [number, number];
      for (const [dx, dy] of sym) {
        const nx = x + dx;
        const ny = y + dy;
        if (nx >= 0 && ny >= 0 && nx < this.width && ny < this.height) seed(nx, ny);
      }
    }

    if (region.size === 0) {
      const status: StageStatus = changes.length > 0 ? 'fixed-point' : 'idle';
      return [{ name: stage.name, kind: stage.kind, status, iterations: 0, changedPixels: changes.length }, changes];
    }

    // Geodesic dilation waves inside A from the current marker, constrained
    // by the current mask. Old values inside A are recomputed from scratch,
    // which both extends new support and revokes removed support.
    const fwd = forwardDeltas(stage.se);
    const reached = new Set<number>();
    let frontier: number[] = [];
    for (const idx of region) {
      const x = idx % this.width;
      const y = Math.floor(idx / this.width);
      if (marker.get(x, y) === 1 && mask.get(x, y) === 1) {
        reached.add(idx);
        frontier.push(idx);
      }
    }
    let iterations = 0;
    while (frontier.length > 0) {
      if (iterations >= stage.maxIterations) {
        // Budget exhausted: keep the previously published output untouched.
        return [
          { name: stage.name, kind: stage.kind, status: 'budget-exhausted', iterations, changedPixels: 0 },
          [],
        ];
      }
      iterations++;
      const next: number[] = [];
      for (const idx of frontier) {
        const x = idx % this.width;
        const y = Math.floor(idx / this.width);
        for (const [dx, dy] of fwd) {
          const nx = x + dx;
          const ny = y + dy;
          if (nx < 0 || ny < 0 || nx >= this.width || ny >= this.height) continue;
          const nIdx = ny * this.width + nx;
          if (!region.has(nIdx) || reached.has(nIdx)) continue;
          if (mask.get(nx, ny) !== 1) continue;
          reached.add(nIdx);
          next.push(nIdx);
        }
      }
      frontier = next;
    }

    for (const idx of region) {
      const x = idx % this.width;
      const y = Math.floor(idx / this.width);
      if (!this.labelPassesAt(stage, x, y)) continue; // pass-through handled above
      writeOut(x, y, reached.has(idx) ? 1 : 0);
    }
    return [
      { name: stage.name, kind: stage.kind, status: 'fixed-point', iterations, changedPixels: changes.length },
      changes,
    ];
  }

  /** Materialize a stage (or source) output for inspection. */
  outputPlane(name?: string): Plane {
    return this.store(name ?? this.graph.output).toPlane();
  }

  cacheStats(): { capacity: number; hotTiles: number; evictions: number } {
    return { capacity: this.cache.capacity, hotTiles: this.cache.size, evictions: this.cache.evictions };
  }
}
