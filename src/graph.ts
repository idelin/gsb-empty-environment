import { GrayImage } from './image.ts';
import { seRadius } from './se.ts';
import type { StructuringElement } from './se.ts';
import type { BoundaryMode, Raster, Rect } from './types.ts';
import { applyLabel, applyLocal, expandRect, fullRect } from './ops.ts';
import { reconstructDilation } from './reconstruct.ts';
import { TileCache, TiledGray } from './tiles.ts';

export interface LocalStageDef {
  kind: 'dilate' | 'erode' | 'open' | 'close';
  id: string;
  /** Stage id or `src:<name>`. */
  input: string;
  se: StructuringElement;
  /** Stage-local boundary mode; defaults keep open anti-extensive / close extensive. */
  boundary?: BoundaryMode;
  /** Stage-local label mask: 0 = passthrough input pixel. */
  label?: Uint8Array | null;
}

export interface ReconstructStageDef {
  kind: 'reconstruct';
  id: string;
  /** Stage id or `src:<name>` supplying the marker. */
  marker: string;
  /** Stage id or `src:<name>` supplying the mask. */
  mask: string;
  /** Propagation neighborhood; fixed-point reach is NOT bounded by its radius. */
  connectivity: StructuringElement;
  /** Max relaxation sweeps; on exhaustion the previous published output is kept. */
  budget?: number;
  /** Stage-local label mask: 0 = pixel stays at min(marker, mask). */
  label?: Uint8Array | null;
}

export type StageDef = LocalStageDef | ReconstructStageDef;

function stageRefs(stage: StageDef): string[] {
  return stage.kind === 'reconstruct' ? [stage.marker, stage.mask] : [stage.input];
}

/** Validate and topologically sort the stage graph. */
export function compileGraph(stages: StageDef[]): StageDef[] {
  const byId = new Map<string, StageDef>();
  for (const s of stages) {
    if (byId.has(s.id)) throw new Error(`duplicate stage id '${s.id}'`);
    byId.set(s.id, s);
  }
  for (const s of stages) {
    for (const r of stageRefs(s)) {
      if (!byId.has(r) && !r.startsWith('src:')) {
        throw new Error(`stage '${s.id}' references unknown input '${r}'`);
      }
    }
  }
  const done = new Set<string>();
  const out: StageDef[] = [];
  const visit = (s: StageDef, stack: Set<string>): void => {
    if (done.has(s.id)) return;
    if (stack.has(s.id)) throw new Error(`cycle in stage graph at '${s.id}'`);
    stack.add(s.id);
    for (const r of stageRefs(s)) {
      const dep = byId.get(r);
      if (dep) visit(dep, stack);
    }
    stack.delete(s.id);
    done.add(s.id);
    out.push(s);
  };
  for (const s of stages) visit(s, new Set<string>());
  return out;
}

export interface StageReport {
  id: string;
  mode: 'full' | 'incremental' | 'none' | 'failed';
  /** False when a reconstruction budget was exhausted and the old output kept. */
  published: boolean;
  converged?: boolean;
  budgetExceeded?: boolean;
  iterations?: number;
  changedPixels: number;
  dirtyRects: Rect[];
}

/**
 * Union-support components (old mask OR new mask) that contain a changed
 * pixel. Recomputing reconstruction inside exactly this region undoes stale
 * propagation (e.g. a severed corridor) without touching unaffected pixels,
 * and is not bounded by any kernel radius.
 */
function affectedRegion(
  oldMask: Raster,
  newMask: Raster,
  changed: number[],
  connectivity: StructuringElement,
): Uint8Array {
  const w = oldMask.width;
  const h = oldMask.height;
  const n = w * h;
  const region = new Uint8Array(n);
  const queue: number[] = [];
  for (const i of changed) {
    if (!region[i]) {
      region[i] = 1;
      queue.push(i);
    }
  }
  while (queue.length > 0) {
    const i = queue.pop()!;
    const x = i % w;
    const y = (i / w) | 0;
    for (const [dx, dy] of connectivity.offsets) {
      const nx = x + dx;
      const ny = y + dy;
      if (nx < 0 || ny < 0 || nx >= w || ny >= h) continue;
      const j = ny * w + nx;
      if (region[j]) continue;
      if (oldMask.get(nx, ny) === 0 && newMask.get(nx, ny) === 0) continue;
      region[j] = 1;
      queue.push(j);
    }
  }
  return region;
}

export interface EngineOptions {
  /** Tile cache capacity in tiles; bounds the resident working set. */
  maxTiles?: number;
  tileSize?: number;
}

export class Engine {
  readonly width: number;
  readonly height: number;
  readonly cache: TileCache;
  private sources = new Map<string, TiledGray>();
  private outputs = new Map<string, TiledGray>();
  private stages: StageDef[] = [];
  /** Last published reconstruct inputs (persisted record, not working set). */
  private reconInputs = new Map<string, { marker: GrayImage; mask: GrayImage }>();
  /** Per-reconstruct-stage candidate buffer; published only on convergence. */
  private reconWork = new Map<string, TiledGray>();
  private reportMap = new Map<string, StageReport>();

  constructor(width: number, height: number, opts: EngineOptions = {}) {
    this.width = width;
    this.height = height;
    this.cache = new TileCache(opts.maxTiles ?? 256, opts.tileSize ?? 16);
  }

  addSource(name: string, img: GrayImage): void {
    if (img.width !== this.width || img.height !== this.height) {
      throw new Error('source size mismatch');
    }
    const tiled = new TiledGray(this.width, this.height, this.cache);
    tiled.loadFrom(img);
    this.sources.set(name, tiled);
  }

  setStages(stages: StageDef[]): void {
    this.stages = compileGraph(stages);
    for (const s of this.stages) {
      if (!this.outputs.has(s.id)) {
        this.outputs.set(s.id, new TiledGray(this.width, this.height, this.cache));
      }
      if (s.kind === 'reconstruct' && !this.reconWork.has(s.id)) {
        this.reconWork.set(s.id, new TiledGray(this.width, this.height, this.cache));
      }
    }
  }

  private rasterOf(ref: string): TiledGray {
    if (ref.startsWith('src:')) {
      const src = this.sources.get(ref.slice(4));
      if (!src) throw new Error(`unknown source '${ref}'`);
      return src;
    }
    const out = this.outputs.get(ref);
    if (!out) throw new Error(`unknown stage '${ref}'`);
    return out;
  }

  private snapshotOf(ref: string): GrayImage {
    return this.rasterOf(ref).snapshot();
  }

  getOutput(ref: string): GrayImage {
    return this.snapshotOf(ref);
  }

  getReport(id: string): StageReport | undefined {
    return this.reportMap.get(id);
  }

  cacheStats() {
    return this.cache.stats();
  }

  /** Full (re)compute of every stage in dependency order. */
  run(): StageReport[] {
    const reports: StageReport[] = [];
    const rect = fullRect(this.width, this.height);
    for (const stage of this.stages) {
      if (stage.kind === 'reconstruct') {
        const marker = this.rasterOf(stage.marker);
        const mask = this.rasterOf(stage.mask);
        const work = this.reconWork.get(stage.id)!;
        const res = reconstructDilation(marker, mask, {
          connectivity: stage.connectivity,
          budget: stage.budget,
          label: stage.label ?? null,
          out: work,
        });
        this.publish(stage.id, work);
        this.reconInputs.set(stage.id, {
          marker: this.snapshotOf(stage.marker),
          mask: this.snapshotOf(stage.mask),
        });
        reports.push({
          id: stage.id,
          mode: 'full',
          published: true,
          converged: res.converged,
          budgetExceeded: !res.converged,
          iterations: res.iterations,
          changedPixels: res.changed,
          dirtyRects: [rect],
        });
      } else {
        const input = this.snapshotOf(stage.input);
        const out = new GrayImage(this.width, this.height);
        applyLocal(stage.kind, input, { se: stage.se, boundary: stage.boundary, rect }, out);
        applyLabel(out, input, stage.label, rect);
        this.outputs.get(stage.id)!.loadFrom(out);
        reports.push({
          id: stage.id,
          mode: 'full',
          published: true,
          changedPixels: this.width * this.height,
          dirtyRects: [rect],
        });
      }
    }
    for (const r of reports) this.reportMap.set(r.id, r);
    return reports;
  }

  /** Copy a candidate buffer into the published output, through the cache. */
  private publish(id: string, work: TiledGray): void {
    const output = this.outputs.get(id)!;
    for (let y = 0; y < this.height; y++) {
      for (let x = 0; x < this.width; x++) {
        output.set(x, y, work.get(x, y));
      }
    }
  }

  /** Apply a local edit to a source image and propagate it incrementally. */
  setPixel(sourceName: string, x: number, y: number, v: number): StageReport[] {
    const src = this.sources.get(sourceName);
    if (!src) throw new Error(`unknown source '${sourceName}'`);
    src.set(x, y, v);
    const dirty = new Map<string, Rect[]>();
    dirty.set(`src:${sourceName}`, [{ x0: x, y0: y, x1: x + 1, y1: y + 1 }]);
    const reports: StageReport[] = [];
    for (const stage of this.stages) {
      const dirtyIn: Rect[] = [];
      for (const r of stageRefs(stage)) {
        const d = dirty.get(r);
        if (d) dirtyIn.push(...d);
      }
      if (dirtyIn.length === 0) {
        reports.push({ id: stage.id, mode: 'none', published: false, changedPixels: 0, dirtyRects: [] });
        continue;
      }
      const report =
        stage.kind === 'reconstruct'
          ? this.updateReconstruct(stage, dirtyIn)
          : this.updateLocal(stage, dirtyIn);
      reports.push(report);
      if (report.published && report.dirtyRects.length > 0) {
        dirty.set(stage.id, report.dirtyRects);
      }
    }
    for (const r of reports) this.reportMap.set(r.id, r);
    return reports;
  }

  /**
   * Recompute only the influenced window of a local stage. Input pixels are
   * read through the bounded tile cache; only the dirty rect is written back.
   */
  private updateLocal(stage: LocalStageDef, dirtyIn: Rect[]): StageReport {
    const input = this.rasterOf(stage.input);
    const output = this.outputs.get(stage.id)!;
    const { rx, ry } = seRadius(stage.se);
    const factor = stage.kind === 'open' || stage.kind === 'close' ? 2 : 1;
    let changed = 0;
    let bx0 = Infinity;
    let by0 = Infinity;
    let bx1 = -1;
    let by1 = -1;
    for (const r of dirtyIn) {
      const dirty = expandRect(r, rx * factor, ry * factor, this.width, this.height);
      const win = expandRect(dirty, rx * factor, ry * factor, this.width, this.height);
      const winImg = new GrayImage(win.x1 - win.x0, win.y1 - win.y0);
      for (let y = win.y0; y < win.y1; y++) {
        for (let x = win.x0; x < win.x1; x++) {
          winImg.set(x - win.x0, y - win.y0, input.get(x, y));
        }
      }
      const localDirty: Rect = {
        x0: dirty.x0 - win.x0,
        y0: dirty.y0 - win.y0,
        x1: dirty.x1 - win.x0,
        y1: dirty.y1 - win.y0,
      };
      const outWin = new GrayImage(winImg.width, winImg.height);
      applyLocal(stage.kind, winImg, { se: stage.se, boundary: stage.boundary, rect: localDirty }, outWin);
      applyLabel(outWin, winImg, stage.label, localDirty);
      for (let y = dirty.y0; y < dirty.y1; y++) {
        for (let x = dirty.x0; x < dirty.x1; x++) {
          const v = outWin.get(x - win.x0, y - win.y0);
          if (v !== output.get(x, y)) {
            output.set(x, y, v);
            changed++;
            if (x < bx0) bx0 = x;
            if (y < by0) by0 = y;
            if (x > bx1) bx1 = x;
            if (y > by1) by1 = y;
          }
        }
      }
    }
    return {
      id: stage.id,
      mode: 'incremental',
      published: changed > 0,
      changedPixels: changed,
      dirtyRects: changed > 0 ? [{ x0: bx0, y0: by0, x1: bx1 + 1, y1: by1 + 1 }] : [],
    };
  }

  private updateReconstruct(stage: ReconstructStageDef, dirtyIn: Rect[]): StageReport {
    const marker = this.rasterOf(stage.marker);
    const mask = this.rasterOf(stage.mask);
    const prev = this.reconInputs.get(stage.id);
    if (!prev) throw new Error(`stage '${stage.id}' has not been run`);
    const output = this.outputs.get(stage.id)!;
    const work = this.reconWork.get(stage.id)!;

    const changedIdx: number[] = [];
    let hasDecrease = false;
    for (const r of dirtyIn) {
      for (let y = r.y0; y < r.y1; y++) {
        for (let x = r.x0; x < r.x1; x++) {
          const i = y * this.width + x;
          const dm = marker.get(x, y) - prev.marker.data[i];
          const dk = mask.get(x, y) - prev.mask.data[i];
          if (dm !== 0 || dk !== 0) {
            changedIdx.push(i);
            if (dm < 0 || dk < 0) hasDecrease = true;
          }
        }
      }
    }
    if (changedIdx.length === 0) {
      return { id: stage.id, mode: 'none', published: false, changedPixels: 0, dirtyRects: [] };
    }

    let region: Uint8Array | null = null;
    if (hasDecrease) {
      // Some input decreased: stale propagation must be revoked. Recompute
      // from the marker inside exactly the affected union-support components.
      region = affectedRegion(prev.mask, mask, changedIdx, stage.connectivity);
      for (let y = 0; y < this.height; y++) {
        for (let x = 0; x < this.width; x++) {
          const i = y * this.width + x;
          let v: number;
          if (region[i]) {
            const mk = marker.get(x, y);
            const mv = mask.get(x, y);
            v = mk < mv ? mk : mv;
          } else {
            v = output.get(x, y);
          }
          work.set(x, y, v);
        }
      }
    }

    const res = reconstructDilation(marker, mask, {
      connectivity: stage.connectivity,
      budget: stage.budget,
      label: stage.label ?? null,
      region,
      seed: hasDecrease ? work : output,
      out: work,
    });

    if (!res.converged) {
      // Budget exhausted: keep the previously published output untouched.
      return {
        id: stage.id,
        mode: 'failed',
        published: false,
        converged: false,
        budgetExceeded: true,
        iterations: res.iterations,
        changedPixels: 0,
        dirtyRects: [],
      };
    }

    // Publish and diff against the previous output, all through the cache.
    let changed = 0;
    let bx0 = Infinity;
    let by0 = Infinity;
    let bx1 = -1;
    let by1 = -1;
    for (let y = 0; y < this.height; y++) {
      for (let x = 0; x < this.width; x++) {
        const v = work.get(x, y);
        if (v !== output.get(x, y)) {
          output.set(x, y, v);
          changed++;
          if (x < bx0) bx0 = x;
          if (y < by0) by0 = y;
          if (x > bx1) bx1 = x;
          if (y > by1) by1 = y;
        }
      }
    }
    this.reconInputs.set(stage.id, {
      marker: this.snapshotOf(stage.marker),
      mask: this.snapshotOf(stage.mask),
    });
    return {
      id: stage.id,
      mode: 'incremental',
      published: changed > 0,
      converged: true,
      budgetExceeded: false,
      iterations: res.iterations,
      changedPixels: changed,
      dirtyRects: changed > 0 ? [{ x0: bx0, y0: by0, x1: bx1 + 1, y1: by1 + 1 }] : [],
    };
  }
}
