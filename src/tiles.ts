import { mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type { Rect } from './kernels.ts';
import { rectHeight, rectWidth } from './kernels.ts';
import { CHANNELS, type MemTracker } from './metrics.ts';

export interface TileGridSpec {
  readonly width: number;
  readonly height: number;
  readonly tileSize: number;
  readonly cols: number;
  readonly rows: number;
}

export function makeGrid(width: number, height: number, tileSize: number): TileGridSpec {
  if (width < 1 || height < 1) throw new Error('grid: empty image');
  if (tileSize < 1) throw new Error('grid: tileSize must be >= 1');
  return {
    width,
    height,
    tileSize,
    cols: Math.ceil(width / tileSize),
    rows: Math.ceil(height / tileSize),
  };
}

export function tileKey(tx: number, ty: number): string {
  return `${tx},${ty}`;
}

export function tileRect(grid: TileGridSpec, tx: number, ty: number): Rect {
  return {
    x0: tx * grid.tileSize,
    y0: ty * grid.tileSize,
    x1: Math.min(grid.width, (tx + 1) * grid.tileSize),
    y1: Math.min(grid.height, (ty + 1) * grid.tileSize),
  };
}

/** Keys of all tiles intersecting `rect` (rect in absolute image coords). */
export function tilesInRect(grid: TileGridSpec, rect: Rect): string[] {
  const tx0 = Math.max(0, Math.floor(rect.x0 / grid.tileSize));
  const ty0 = Math.max(0, Math.floor(rect.y0 / grid.tileSize));
  const tx1 = Math.min(grid.cols - 1, Math.floor((rect.x1 - 1) / grid.tileSize));
  const ty1 = Math.min(grid.rows - 1, Math.floor((rect.y1 - 1) / grid.tileSize));
  const keys: string[] = [];
  for (let ty = ty0; ty <= ty1; ty++) {
    for (let tx = tx0; tx <= tx1; tx++) keys.push(tileKey(tx, ty));
  }
  return keys;
}

interface TileEntry {
  rect: Rect;
  data: Float32Array | null; // null while spilled
  spillPath: string | null;
  bytes: number;
  lastUsed: number;
}

/**
 * LRU tile cache with disk spill. Tiles exceeding the memory budget are
 * written to a spill directory; `dispose()` removes every spill file.
 * Cached bytes are accounted in the shared MemTracker so the measured
 * peak memory includes the cache.
 */
export class TileStore {
  private entries = new Map<string, TileEntry>();
  private tick = 0;
  private cachedBytes = 0;
  spillCount = 0;
  loadCount = 0;
  readonly spillDir: string;
  readonly capacityBytes: number;
  private readonly tracker?: MemTracker;

  constructor(
    spillDir: string,
    capacityBytes: number,
    tracker?: MemTracker,
  ) {
    this.spillDir = spillDir;
    this.capacityBytes = capacityBytes;
    this.tracker = tracker;
    mkdirSync(spillDir, { recursive: true });
  }

  put(key: string, rect: Rect, data: Float32Array): void {
    const existing = this.entries.get(key);
    if (existing) this.remove(key);
    const bytes = data.byteLength;
    this.evictFor(bytes, null);
    const entry: TileEntry = {
      rect,
      data,
      spillPath: null,
      bytes,
      lastUsed: ++this.tick,
    };
    this.entries.set(key, entry);
    this.cachedBytes += bytes;
    this.tracker?.alloc(bytes);
  }

  get(key: string): { rect: Rect; data: Float32Array } {
    const entry = this.entries.get(key);
    if (!entry) throw new Error(`TileStore: missing tile ${key}`);
    entry.lastUsed = ++this.tick;
    if (entry.data === null) {
      this.evictFor(entry.bytes, key);
      const raw = readFileSync(entry.spillPath!);
      entry.data = new Float32Array(raw.buffer, raw.byteOffset, entry.bytes / 4);
      rmSync(entry.spillPath!);
      entry.spillPath = null;
      this.cachedBytes += entry.bytes;
      this.tracker?.alloc(entry.bytes);
      this.loadCount++;
    }
    return { rect: entry.rect, data: entry.data };
  }

  has(key: string): boolean {
    return this.entries.has(key);
  }

  remove(key: string): void {
    const entry = this.entries.get(key);
    if (!entry) return;
    if (entry.data !== null) {
      this.cachedBytes -= entry.bytes;
      this.tracker?.release(entry.bytes);
    }
    if (entry.spillPath !== null) rmSync(entry.spillPath, { force: true });
    this.entries.delete(key);
  }

  keys(): string[] {
    return [...this.entries.keys()];
  }

  removeNamespace(prefix: string): void {
    for (const key of [...this.entries.keys()]) {
      if (key.startsWith(prefix)) this.remove(key);
    }
  }

  /** Spill LRU tiles until `incoming` more bytes fit within capacity. */
  private evictFor(incoming: number, protectKey: string | null): void {
    while (this.cachedBytes + incoming > this.capacityBytes) {
      let victimKey: string | null = null;
      let victimTick = Infinity;
      for (const [key, entry] of this.entries) {
        if (key === protectKey || entry.data === null) continue;
        if (entry.lastUsed < victimTick) {
          victimTick = entry.lastUsed;
          victimKey = key;
        }
      }
      if (victimKey === null) return; // nothing evictable; caller must have budgeted
      const victim = this.entries.get(victimKey)!;
      const path = join(this.spillDir, `tile-${victimKey.replace(',', '_')}.f32`);
      writeFileSync(path, Buffer.from(victim.data!.buffer, victim.data!.byteOffset, victim.bytes));
      victim.data = null;
      victim.spillPath = path;
      this.cachedBytes -= victim.bytes;
      this.tracker?.release(victim.bytes);
      this.spillCount++;
    }
  }

  /** Release all memory and delete every spill file plus the spill dir. */
  dispose(): void {
    for (const key of [...this.entries.keys()]) this.remove(key);
    try {
      for (const f of readdirSync(this.spillDir)) rmSync(join(this.spillDir, f), { force: true });
      rmSync(this.spillDir, { recursive: true, force: true });
    } catch {
      // best effort cleanup
    }
  }
}

/**
 * A raster stored as tiles in a TileStore. Regions spanning many tiles are
 * assembled on demand; neighbour tiles provide cross-tile samples so tile
 * edges are never treated as image edges.
 */
export class TiledRaster {
  readonly grid: TileGridSpec;
  readonly store: TileStore;
  readonly namespace: string;

  constructor(
    store: TileStore,
    namespace: string,
    width: number,
    height: number,
    tileSize: number,
  ) {
    this.store = store;
    this.namespace = namespace;
    this.grid = makeGrid(width, height, tileSize);
  }

  private key(tx: number, ty: number): string {
    return `${this.namespace}:${tx},${ty}`;
  }

  writeTile(tx: number, ty: number, data: Float32Array): void {
    this.store.put(this.key(tx, ty), tileRect(this.grid, tx, ty), data);
  }

  /** Store keys (namespace-prefixed) of all tiles of this raster. */
  keys(): string[] {
    const prefix = `${this.namespace}:`;
    return this.store.keys().filter((k) => k.startsWith(prefix));
  }

  writeFullRaster(width: number, height: number, data: Float32Array): void {
    for (let ty = 0; ty < this.grid.rows; ty++) {
      for (let tx = 0; tx < this.grid.cols; tx++) {
        const r = tileRect(this.grid, tx, ty);
        const w = rectWidth(r);
        const h = rectHeight(r);
        const tile = new Float32Array(w * h * CHANNELS);
        for (let y = 0; y < h; y++) {
          const srcOff = ((r.y0 + y) * width + r.x0) * CHANNELS;
          tile.set(data.subarray(srcOff, srcOff + w * CHANNELS), y * w * CHANNELS);
        }
        this.writeTile(tx, ty, tile);
      }
    }
  }

  /** Assemble an absolute image rect (must be clipped to the image). */
  readRegion(rect: Rect, tracker?: MemTracker): { data: Float32Array; rect: Rect } {
    const w = rectWidth(rect);
    const h = rectHeight(rect);
    if (w <= 0 || h <= 0) throw new Error('readRegion: empty rect');
    const out = new Float32Array(w * h * CHANNELS);
    tracker?.alloc(out.byteLength);
    for (let ty = 0; ty < this.grid.rows; ty++) {
      for (let tx = 0; tx < this.grid.cols; tx++) {
        const tr = tileRect(this.grid, tx, ty);
        const ix0 = Math.max(rect.x0, tr.x0);
        const iy0 = Math.max(rect.y0, tr.y0);
        const ix1 = Math.min(rect.x1, tr.x1);
        const iy1 = Math.min(rect.y1, tr.y1);
        if (ix0 >= ix1 || iy0 >= iy1) continue;
        const { rect: storedRect, data } = this.store.get(this.key(tx, ty));
        const tw = rectWidth(storedRect);
        for (let y = iy0; y < iy1; y++) {
          const srcOff = ((y - storedRect.y0) * tw + (ix0 - storedRect.x0)) * CHANNELS;
          const dstOff = ((y - rect.y0) * w + (ix0 - rect.x0)) * CHANNELS;
          out.set(data.subarray(srcOff, srcOff + (ix1 - ix0) * CHANNELS), dstOff);
        }
      }
    }
    return { data: out, rect };
  }

  releaseRegion(buf: { data: Float32Array }, tracker?: MemTracker): void {
    tracker?.release(buf.data.byteLength);
  }

  /** Assemble the whole image (used to read back final results). */
  toRaster(): { width: number; height: number; data: Float32Array } {
    const region = this.readRegion({
      x0: 0,
      y0: 0,
      x1: this.grid.width,
      y1: this.grid.height,
    });
    return { width: this.grid.width, height: this.grid.height, data: region.data };
  }
}
