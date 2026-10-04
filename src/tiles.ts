import { makePlane, type Plane } from './types.ts';

/**
 * Bounded decoded-tile cache shared by all tile stores. Cold tiles live
 * RLE-compressed inside their store; only up to `capacity` decoded tiles are
 * hot at any moment, across the whole pipeline. Eviction re-encodes the tile
 * into its store, so correctness never depends on the cache size.
 */
export class TileCache {
  readonly capacity: number;
  private lru = new Map<string, HotTile>();
  evictions = 0;

  constructor(capacity: number) {
    if (capacity < 1) throw new Error('tile cache capacity must be >= 1');
    this.capacity = capacity;
  }

  get size(): number {
    return this.lru.size;
  }

  /** Fetch a hot tile, or materialize it from the store (evicting as needed). */
  access(store: TileStore, tx: number, ty: number): Uint8Array {
    const key = store.id + ':' + tx + ':' + ty;
    const hit = this.lru.get(key);
    if (hit) {
      this.lru.delete(key);
      this.lru.set(key, hit); // refresh recency
      return hit.data;
    }
    const data = store.decodeTile(tx, ty);
    this.lru.set(key, { store, tx, ty, data });
    if (this.lru.size > this.capacity) {
      const oldestKey = this.lru.keys().next().value as string;
      const oldest = this.lru.get(oldestKey) as HotTile;
      oldest.store.encodeTile(oldest.tx, oldest.ty, oldest.data);
      this.lru.delete(oldestKey);
      this.evictions++;
    }
    return data;
  }

  /** Drop all hot tiles of a store (used when a store is discarded). */
  dropStore(store: TileStore): void {
    for (const [key, tile] of this.lru) {
      if (tile.store === store) {
        store.encodeTile(tile.tx, tile.ty, tile.data);
        this.lru.delete(key);
      }
    }
  }
}

interface HotTile {
  store: TileStore;
  tx: number;
  ty: number;
  data: Uint8Array;
}

let nextStoreId = 1;

export class TileStore {
  readonly id = nextStoreId++;
  readonly width: number;
  readonly height: number;
  readonly tileSize: number;
  readonly tilesX: number;
  readonly tilesY: number;
  readonly cache: TileCache;
  private cold = new Map<number, Uint8Array>();

  constructor(width: number, height: number, cache: TileCache, tileSize = 16) {
    this.cache = cache;
    this.width = width;
    this.height = height;
    this.tileSize = tileSize;
    this.tilesX = Math.ceil(width / tileSize);
    this.tilesY = Math.ceil(height / tileSize);
    for (let ty = 0; ty < this.tilesY; ty++)
      for (let tx = 0; tx < this.tilesX; tx++)
        this.cold.set(ty * this.tilesX + tx, rleEncode(new Uint8Array(tileSize * tileSize)));
  }

  static fromPlane(plane: Plane, cache: TileCache, tileSize = 16): TileStore {
    const store = new TileStore(plane.width, plane.height, cache, tileSize);
    for (let y = 0; y < plane.height; y++)
      for (let x = 0; x < plane.width; x++) store.set(x, y, plane.data[y * plane.width + x] > 0 ? 1 : 0);
    return store;
  }

  get(x: number, y: number): number {
    const tx = Math.floor(x / this.tileSize);
    const ty = Math.floor(y / this.tileSize);
    const tile = this.cache.access(this, tx, ty);
    return tile[(y - ty * this.tileSize) * this.tileSize + (x - tx * this.tileSize)];
  }

  set(x: number, y: number, value: number): void {
    const tx = Math.floor(x / this.tileSize);
    const ty = Math.floor(y / this.tileSize);
    const tile = this.cache.access(this, tx, ty);
    tile[(y - ty * this.tileSize) * this.tileSize + (x - tx * this.tileSize)] = value > 0 ? 1 : 0;
  }

  decodeTile(tx: number, ty: number): Uint8Array {
    const enc = this.cold.get(ty * this.tilesX + tx);
    if (!enc) throw new Error(`missing tile ${tx},${ty}`);
    return rleDecode(enc, this.tileSize * this.tileSize);
  }

  encodeTile(tx: number, ty: number, data: Uint8Array): void {
    this.cold.set(ty * this.tilesX + tx, rleEncode(data));
  }

  /** Materialize the whole plane (validation / reporting only). */
  toPlane(): Plane {
    const plane = makePlane(this.width, this.height);
    for (let y = 0; y < this.height; y++)
      for (let x = 0; x < this.width; x++) plane.data[y * this.width + x] = this.get(x, y);
    return plane;
  }

  get coldBytes(): number {
    let n = 0;
    for (const enc of this.cold.values()) n += enc.length;
    return n;
  }
}

/** PackBits-style RLE: [count, value] pairs, count in 1..255. */
export function rleEncode(data: Uint8Array): Uint8Array {
  const out: number[] = [];
  let i = 0;
  while (i < data.length) {
    const v = data[i];
    let run = 1;
    while (i + run < data.length && data[i + run] === v && run < 255) run++;
    out.push(run, v);
    i += run;
  }
  return Uint8Array.from(out);
}

export function rleDecode(enc: Uint8Array, length: number): Uint8Array {
  const out = new Uint8Array(length);
  let o = 0;
  for (let i = 0; i < enc.length; i += 2) {
    out.fill(enc[i + 1], o, o + enc[i]);
    o += enc[i];
  }
  if (o !== length) throw new Error('corrupt RLE tile');
  return out;
}
