import { GrayImage } from './image.ts';

export interface CacheStats {
  hits: number;
  misses: number;
  evictions: number;
  writebacks: number;
  resident: number;
  maxTiles: number;
}

interface CacheEntry {
  ownerId: number;
  tx: number;
  ty: number;
  data: Uint8Array;
  dirty: boolean;
}

/**
 * Bounded LRU cache of tiles fronting the persistent per-image tile stores.
 * All pixel traffic of the engine goes through this cache, so the resident
 * working set never exceeds `maxTiles` tiles; evicted dirty tiles are written
 * back to their owner's store and reloaded on demand.
 */
export class TileCache {
  readonly maxTiles: number;
  readonly tileSize: number;
  private lru = new Map<string, CacheEntry>();
  private hits = 0;
  private misses = 0;
  private evictions = 0;
  private writebacks = 0;

  constructor(maxTiles = 64, tileSize = 16) {
    if (maxTiles < 1) throw new Error('maxTiles must be >= 1');
    this.maxTiles = maxTiles;
    this.tileSize = tileSize;
  }

  private key(ownerId: number, tx: number, ty: number): string {
    return `${ownerId}:${tx}:${ty}`;
  }

  acquire(owner: TiledGray, tx: number, ty: number): CacheEntry {
    const key = this.key(owner.id, tx, ty);
    const hit = this.lru.get(key);
    if (hit) {
      this.lru.delete(key);
      this.lru.set(key, hit);
      this.hits++;
      return hit;
    }
    this.misses++;
    const data = new Uint8Array(this.tileSize * this.tileSize);
    const stored = owner.store.get(`${tx}:${ty}`);
    if (stored) data.set(stored);
    const entry: CacheEntry = { ownerId: owner.id, tx, ty, data, dirty: false };
    this.lru.set(key, entry);
    this.evictIfNeeded();
    return entry;
  }

  private evictIfNeeded(): void {
    while (this.lru.size > this.maxTiles) {
      const oldest = this.lru.keys().next();
      if (oldest.done) break;
      const key = oldest.value;
      const entry = this.lru.get(key)!;
      if (entry.dirty) {
        const owner = TiledGray.registry.get(entry.ownerId);
        if (owner) {
          owner.store.set(`${entry.tx}:${entry.ty}`, new Uint8Array(entry.data));
          this.writebacks++;
        }
      }
      this.lru.delete(key);
      this.evictions++;
    }
  }

  /** Drop all cached tiles of an owner without writeback (owner data replaced). */
  dropOwner(owner: TiledGray): void {
    for (const [key, entry] of this.lru) {
      if (entry.ownerId === owner.id) this.lru.delete(key);
    }
  }

  /** Write back all dirty tiles of an owner (keeps them cached). */
  flushOwner(owner: TiledGray): void {
    for (const entry of this.lru.values()) {
      if (entry.ownerId === owner.id && entry.dirty) {
        owner.store.set(`${entry.tx}:${entry.ty}`, new Uint8Array(entry.data));
        entry.dirty = false;
        this.writebacks++;
      }
    }
  }

  stats(): CacheStats {
    return {
      hits: this.hits,
      misses: this.misses,
      evictions: this.evictions,
      writebacks: this.writebacks,
      resident: this.lru.size,
      maxTiles: this.maxTiles,
    };
  }
}

let nextOwnerId = 1;

/** A grayscale image stored as tiles behind the shared bounded TileCache. */
export class TiledGray {
  static registry = new Map<number, TiledGray>();

  readonly id: number;
  readonly width: number;
  readonly height: number;
  readonly cache: TileCache;
  /** Persistent backing store ("disk"); the cache is only a working set. */
  readonly store = new Map<string, Uint8Array>();

  constructor(width: number, height: number, cache: TileCache) {
    this.id = nextOwnerId++;
    this.width = width;
    this.height = height;
    this.cache = cache;
    TiledGray.registry.set(this.id, this);
  }

  get(x: number, y: number): number {
    const ts = this.cache.tileSize;
    const tx = (x / ts) | 0;
    const ty = (y / ts) | 0;
    const entry = this.cache.acquire(this, tx, ty);
    return entry.data[(y % ts) * ts + (x % ts)];
  }

  set(x: number, y: number, v: number): void {
    const ts = this.cache.tileSize;
    const tx = (x / ts) | 0;
    const ty = (y / ts) | 0;
    const entry = this.cache.acquire(this, tx, ty);
    entry.data[(y % ts) * ts + (x % ts)] = v;
    entry.dirty = true;
  }

  /** Replace the whole image content (used when publishing a stage output). */
  loadFrom(img: GrayImage): void {
    this.cache.dropOwner(this);
    this.store.clear();
    const ts = this.cache.tileSize;
    for (let y = 0; y < this.height; y++) {
      for (let x = 0; x < this.width; x++) {
        const key = `${(x / ts) | 0}:${(y / ts) | 0}`;
        let tile = this.store.get(key);
        if (!tile) {
          tile = new Uint8Array(ts * ts);
          this.store.set(key, tile);
        }
        tile[(y % ts) * ts + (x % ts)] = img.data[y * this.width + x];
      }
    }
  }

  /** Materialize a full copy; reads stream through the bounded cache. */
  snapshot(): GrayImage {
    const out = new GrayImage(this.width, this.height);
    for (let y = 0; y < this.height; y++) {
      for (let x = 0; x < this.width; x++) {
        out.data[y * this.width + x] = this.get(x, y);
      }
    }
    return out;
  }
}
