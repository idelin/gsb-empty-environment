/**
 * Chunked, versioned storage for the distance field.
 *
 * The grid is split into fixed-size square chunks ("pages"). Every chunk
 * version is immutable; updates build new chunk objects. A `Version` is an
 * atomic snapshot: the field only ever exposes a fully consistent version,
 * so an in-progress incremental repair is never visible to readers (no
 * mixed-version reads). Publishing is a single pointer swap.
 *
 * Old versions stay queryable until released. Chunk pages are shared
 * between versions by reference counting; `collectGarbage()` reclaims
 * pages no longer referenced by any live version or working copy.
 */

export const CHUNK_SHIFT = 4; // 16x16 cells per chunk
export const CHUNK_SIZE = 1 << CHUNK_SHIFT;

export class Chunk {
  readonly size: number;
  dist: Float64Array;
  srcX: Int32Array;
  srcY: Int32Array;
  obstacle: Uint8Array;
  /** Number of live references (versions + at most one working copy). */
  refs = 0;

  constructor(size: number) {
    this.size = size;
    const n = size * size;
    this.dist = new Float64Array(n);
    this.srcX = new Int32Array(n);
    this.srcY = new Int32Array(n);
    this.obstacle = new Uint8Array(n);
  }

  clone(): Chunk {
    const c = new Chunk(this.size);
    c.dist.set(this.dist);
    c.srcX.set(this.srcX);
    c.srcY.set(this.srcY);
    c.obstacle.set(this.obstacle);
    return c;
  }
}

export type ChunkTable = ReadonlyMap<number, Chunk>;

export interface Version {
  readonly id: number;
  readonly chunks: ChunkTable;
}

export interface Cell {
  dist: number;
  srcX: number;
  srcY: number;
  obstacle: boolean;
}

export const OUT_OF_BOUNDS: Cell = Object.freeze({
  dist: Number.POSITIVE_INFINITY,
  srcX: -1,
  srcY: -1,
  obstacle: false,
});

export function chunkKey(cx: number, cy: number): number {
  // Supports negative chunk coordinates (negative world cells).
  return (cx + 0x8000) * 0x10000 + (cy + 0x8000);
}

/**
 * The versioned store. All reads go through `snapshot()` (current live
 * version) or a retained older `Version`.
 */
export class VersionedField {
  readonly width: number;
  readonly height: number;
  /** World coordinates of local cell (0,0); may be negative. */
  readonly originX: number;
  readonly originY: number;

  private live: Version;
  private nextVersionId = 1;
  private retained = new Map<Version, number>();
  /** All pages ever allocated and not yet reclaimed. */
  private pages = new Set<Chunk>();

  constructor(width: number, height: number, originX = 0, originY = 0) {
    this.width = width;
    this.height = height;
    this.originX = originX;
    this.originY = originY;
    this.live = { id: 0, chunks: new Map() };
  }

  /** Current published version. */
  snapshot(): Version {
    return this.live;
  }

  /**
   * Pin a version so it stays queryable; call `release` when done.
   * Retaining the live version is allowed and counted like any other
   * reference, so a later publish cannot drop pages out from under it.
   */
  retain(v: Version): Version {
    this.retained.set(v, (this.retained.get(v) ?? 0) + 1);
    for (const c of v.chunks.values()) c.refs++;
    return v;
  }

  release(v: Version): void {
    const n = this.retained.get(v) ?? 0;
    if (n <= 1) this.retained.delete(v);
    else this.retained.set(v, n - 1);
    for (const c of v.chunks.values()) c.refs--;
  }

  /**
   * Atomically publish a new version built from `chunks`. Pages that are
   * no longer referenced afterwards become reclaimable garbage.
   */
  publish(chunks: Map<number, Chunk>): Version {
    const table = new Map(chunks); // copy: the caller may reuse/clear its map
    for (const c of table.values()) {
      if (c.refs === 0) this.pages.add(c);
      c.refs++;
    }
    const old = this.live;
    for (const c of old.chunks.values()) c.refs--;
    this.live = { id: this.nextVersionId++, chunks: table };
    return this.live;
  }

  /** Reclaim pages not referenced by any live/retained version. */
  collectGarbage(): number {
    let freed = 0;
    for (const c of this.pages) {
      if (c.refs <= 0) {
        this.pages.delete(c);
        freed++;
      }
    }
    return freed;
  }

  get pageCount(): number {
    return this.pages.size;
  }

  /** Read one cell from a specific version. World coordinates. */
  getCell(v: Version, wx: number, wy: number): Cell {
    const lx = wx - this.originX;
    const ly = wy - this.originY;
    if (lx < 0 || ly < 0 || lx >= this.width || ly >= this.height) {
      return OUT_OF_BOUNDS;
    }
    const cx = lx >> CHUNK_SHIFT;
    const cy = ly >> CHUNK_SHIFT;
    const chunk = v.chunks.get(chunkKey(cx, cy));
    if (!chunk) {
      // Chunk was never materialized: no obstacle can live there.
      return OUT_OF_BOUNDS;
    }
    const i = (ly & (CHUNK_SIZE - 1)) * CHUNK_SIZE + (lx & (CHUNK_SIZE - 1));
    return {
      dist: chunk.dist[i],
      srcX: chunk.srcX[i] < 0 ? -1 : chunk.srcX[i] + this.originX,
      srcY: chunk.srcY[i] < 0 ? -1 : chunk.srcY[i] + this.originY,
      obstacle: chunk.obstacle[i] === 1,
    };
  }
}
