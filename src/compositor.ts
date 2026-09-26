import { linearToSrgb, srgbToLinear, transferFn, type ColorSpace } from './color.ts';
import { compilePlan, type Plan } from './graph.ts';
import { layerBounds, type Layer, type Rect } from './layer.ts';
import { BufferPool } from './pixels.ts';

interface TileEntry {
  buf: Float32Array; // 工作空间、预乘 RGBA
  refs: number;
}

interface Version {
  id: number;
  entries: (TileEntry | null)[];
  workingSpace: ColorSpace;
}

export interface CommitResult {
  version: number;
  tilesRepainted: number;
  tilesTotal: number;
}

export interface PoolStats {
  allocations: number;
  reuses: number;
  live: number;
}

export class Compositor {
  readonly width: number;
  readonly height: number;
  readonly tileSize: number;
  readonly tilesX: number;
  readonly tilesY: number;
  readonly outputSpace: ColorSpace;
  private pool: BufferPool;
  private layers = new Map<string, Layer>();
  private dirtyRects: Rect[] = [];
  private versions = new Map<number, Version>();
  private current: Version | null = null;
  private nextVersionId = 1;

  constructor(opts: { width: number; height: number; tileSize?: number; outputSpace?: ColorSpace }) {
    this.width = opts.width;
    this.height = opts.height;
    this.tileSize = opts.tileSize ?? 16;
    this.tilesX = Math.ceil(this.width / this.tileSize);
    this.tilesY = Math.ceil(this.height / this.tileSize);
    this.outputSpace = opts.outputSpace ?? 'srgb';
    this.pool = new BufferPool(this.tileSize * this.tileSize * 4);
  }

  get currentVersion(): number {
    return this.current?.id ?? 0;
  }

  stats(): PoolStats {
    return { allocations: this.pool.allocations, reuses: this.pool.reuses, live: this.pool.live };
  }

  // 插入或替换图层；记录旧+新包围盒作为保守脏区
  upsertLayer(layer: Layer): void {
    const prev = this.layers.get(layer.id);
    if (prev) {
      const b = layerBounds(prev, this.width, this.height);
      if (b) this.dirtyRects.push(b);
    }
    this.layers.set(layer.id, layer);
    const b = layerBounds(layer, this.width, this.height);
    if (b) this.dirtyRects.push(b);
  }

  removeLayer(id: string): void {
    const prev = this.layers.get(id);
    if (!prev) return;
    const b = layerBounds(prev, this.width, this.height);
    if (b) this.dirtyRects.push(b);
    this.layers.delete(id);
  }

  // 编译计划并渲染脏瓦片；任何失败都不影响已发布版本（原子发布）
  commit(): CommitResult {
    const plan = compilePlan([...this.layers.values()], this.width, this.height);
    const tilesTotal = this.tilesX * this.tilesY;
    const prev = this.current;

    let dirtyTiles: Set<number>;
    if (!prev || prev.workingSpace !== plan.workingSpace) {
      dirtyTiles = new Set(Array.from({ length: tilesTotal }, (_, i) => i));
    } else {
      dirtyTiles = new Set<number>();
      for (const r of this.dirtyRects) {
        for (const t of this.tilesForRect(r)) dirtyTiles.add(t);
      }
    }

    const oldEntries: (TileEntry | null)[] = prev ? prev.entries : new Array(tilesTotal).fill(null);
    const newEntries: (TileEntry | null)[] = new Array(tilesTotal).fill(null);
    const acquired: TileEntry[] = [];
    try {
      for (let i = 0; i < tilesTotal; i++) {
        if (!dirtyTiles.has(i)) {
          newEntries[i] = oldEntries[i];
          continue;
        }
        const entry: TileEntry = { buf: this.pool.acquire(), refs: 0 };
        acquired.push(entry); // 先登记再渲染，渲染抛错时才能归还
        this.renderTile(entry.buf, i % this.tilesX, Math.floor(i / this.tilesX), plan);
        newEntries[i] = entry;
      }
    } catch (err) {
      // 渲染中途失败：归还已分配的缓冲，旧版本原样保留，不发布半幅图
      for (const e of acquired) this.pool.release(e.buf);
      throw err;
    }

    for (const e of newEntries) if (e) e.refs++;
    const version: Version = { id: this.nextVersionId++, entries: newEntries, workingSpace: plan.workingSpace };
    this.versions.set(version.id, version);
    this.current = version;
    this.dirtyRects = [];
    return { version: version.id, tilesRepainted: acquired.length, tilesTotal };
  }

  releaseVersion(id: number): void {
    const v = this.versions.get(id);
    if (!v) return;
    if (v === this.current) throw new Error('cannot release current version');
    for (const e of v.entries) {
      if (e) {
        e.refs--;
        if (e.refs === 0) this.pool.release(e.buf);
      }
    }
    this.versions.delete(id);
  }

  // 读取指定版本（含旧快照）的像素，返回输出空间的非预乘 RGBA
  readPixel(version: number, x: number, y: number): [number, number, number, number] {
    const v = this.versions.get(version);
    if (!v) throw new Error(`unknown version: ${version}`);
    if (x < 0 || y < 0 || x >= this.width || y >= this.height) throw new RangeError('pixel out of bounds');
    const ti = Math.floor(y / this.tileSize) * this.tilesX + Math.floor(x / this.tileSize);
    const e = v.entries[ti];
    if (!e) return [0, 0, 0, 0];
    const i = ((y % this.tileSize) * this.tileSize + (x % this.tileSize)) * 4;
    let r = e.buf[i];
    let g = e.buf[i + 1];
    let b = e.buf[i + 2];
    const a = e.buf[i + 3];
    if (a > 0) {
      r /= a;
      g /= a;
      b /= a;
    }
    if (v.workingSpace !== this.outputSpace) {
      const f = v.workingSpace === 'linear' ? linearToSrgb : srgbToLinear;
      r = f(r);
      g = f(g);
      b = f(b);
    }
    return [r, g, b, a];
  }

  snapshotPixels(version: number): Float32Array {
    const out = new Float32Array(this.width * this.height * 4);
    for (let y = 0; y < this.height; y++) {
      for (let x = 0; x < this.width; x++) {
        out.set(this.readPixel(version, x, y), (y * this.width + x) * 4);
      }
    }
    return out;
  }

  private tilesForRect(r: Rect): number[] {
    const x0 = Math.max(0, Math.floor(r.x / this.tileSize));
    const y0 = Math.max(0, Math.floor(r.y / this.tileSize));
    const x1 = Math.min(this.tilesX - 1, Math.floor((r.x + r.w - 1) / this.tileSize));
    const y1 = Math.min(this.tilesY - 1, Math.floor((r.y + r.h - 1) / this.tileSize));
    const out: number[] = [];
    for (let ty = y0; ty <= y1; ty++) {
      for (let tx = x0; tx <= x1; tx++) out.push(ty * this.tilesX + tx);
    }
    return out;
  }

  private renderTile(buf: Float32Array, tx: number, ty: number, plan: Plan): void {
    buf.fill(0);
    const x0 = tx * this.tileSize;
    const y0 = ty * this.tileSize;
    const x1 = Math.min(this.width, x0 + this.tileSize);
    const y1 = Math.min(this.height, y0 + this.tileSize);

    for (const step of plan.steps) {
      const L = step.layer;
      const crop = L.crop ?? { x: 0, y: 0, w: L.width, h: L.height };
      const ix0 = Math.max(x0, step.bounds.x);
      const iy0 = Math.max(y0, step.bounds.y);
      const ix1 = Math.min(x1, step.bounds.x + step.bounds.w);
      const iy1 = Math.min(y1, step.bounds.y + step.bounds.h);
      if (ix0 >= ix1 || iy0 >= iy1) continue;

      const convert = step.convert ? transferFn(L.colorSpace) : null;
      for (let cy = iy0; cy < iy1; cy++) {
        for (let cx = ix0; cx < ix1; cx++) {
          const lx = crop.x + (cx - L.offsetX);
          const ly = crop.y + (cy - L.offsetY);
          const si = (ly * L.width + lx) * 4;
          let r = L.pixels[si];
          let g = L.pixels[si + 1];
          let b = L.pixels[si + 2];
          const a = L.pixels[si + 3];
          if (convert) {
            r = convert(r);
            g = convert(g);
            b = convert(b);
          }
          if (step.premultiply) {
            r *= a;
            g *= a;
            b *= a;
          }
          const sa = a * L.opacity;
          const sr = r * L.opacity;
          const sg = g * L.opacity;
          const sb = b * L.opacity;
          const di = ((cy - y0) * this.tileSize + (cx - x0)) * 4;
          switch (L.blend) {
            case 'source-over': {
              const k = 1 - sa;
              buf[di] = sr + buf[di] * k;
              buf[di + 1] = sg + buf[di + 1] * k;
              buf[di + 2] = sb + buf[di + 2] * k;
              buf[di + 3] = sa + buf[di + 3] * k;
              break;
            }
            case 'lighter': {
              buf[di] += sr;
              buf[di + 1] += sg;
              buf[di + 2] += sb;
              buf[di + 3] += sa;
              break;
            }
            case 'multiply': {
              const da = buf[di + 3];
              const dr = da > 0 ? buf[di] / da : 0;
              const dg = da > 0 ? buf[di + 1] / da : 0;
              const db = da > 0 ? buf[di + 2] / da : 0;
              const k = 1 - sa;
              buf[di] = sr * dr + buf[di] * k;
              buf[di + 1] = sg * dg + buf[di + 1] * k;
              buf[di + 2] = sb * db + buf[di + 2] * k;
              buf[di + 3] = sa + da * k;
              break;
            }
          }
        }
      }
    }
  }
}
