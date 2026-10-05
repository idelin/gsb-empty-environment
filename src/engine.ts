/**
 * Streaming query engine.
 *
 * - Evaluates and/or expressions over sharded set files as a pull-based
 *   multi-way merge, emitting strictly increasing uint32 ids.
 * - Never expands a whole set or the whole result in memory: each leaf set
 *   pins at most one decoded block at a time, so peak decoded bytes is
 *   bounded by (blocks in flight) * BLOCK_SIZE * 4.
 * - Per-tenant Session quota caps concurrently pinned decoded bytes; if the
 *   quota cannot fit a single minimal block the query is rejected explicitly.
 * - Cursors make pages resumable and idempotent: re-issuing the same cursor
 *   reproduces the same page, and cancelling mid-page is safe because resume
 *   state lives entirely in the cursor.
 */
import { Expr, exprHash, exprSets } from './expr';
import { CursorPayload, assertCursorBinding, decodeCursor, encodeCursor } from './cursor';
import { OpenedSet, SetStore } from './store';

export class QuotaExceededError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'QuotaExceededError';
  }
}

export class IllegalArgumentError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'IllegalArgumentError';
  }
}

/** Per-query memory accounting for one tenant. */
export class Session {
  usedBytes = 0;
  peakBytes = 0;
  constructor(readonly tenant: string, readonly quotaBytes: number) {
    if (!Number.isInteger(quotaBytes) || quotaBytes < 0) {
      throw new IllegalArgumentError('quotaBytes must be a non-negative integer');
    }
  }

  pin(bytes: number, what: string): void {
    if (this.usedBytes + bytes > this.quotaBytes) {
      throw new QuotaExceededError(
        'tenant "' + this.tenant + '" decoded-bytes quota exceeded: need ' + bytes +
        ' bytes for ' + what + ' but only ' + (this.quotaBytes - this.usedBytes) +
        ' of ' + this.quotaBytes + ' bytes remain (a single minimal block must fit the quota)',
      );
    }
    this.usedBytes += bytes;
    if (this.usedBytes > this.peakBytes) this.peakBytes = this.usedBytes;
  }

  unpin(bytes: number): void {
    this.usedBytes -= bytes;
    if (this.usedBytes < 0) this.usedBytes = 0;
  }
}

interface IdStream {
  next(): number | null;
  close(): void;
}

/** Streams one set's ids strictly above `afterId`, shard by shard, block by block. */
class SetStream implements IdStream {
  private shardIdx = 0;
  private blockIdx = 0;
  private block: Uint32Array | null = null;
  private blockBytes = 0;
  private pos = 0;
  private done = false;

  constructor(
    private readonly store: SetStore,
    private readonly opened: OpenedSet,
    private readonly session: Session,
    private readonly afterId: number,
  ) {
    this.skipEmptyShards();
  }

  private skipEmptyShards(): void {
    const shards = this.opened.header.shards;
    while (this.shardIdx < shards.length) {
      const s = shards[this.shardIdx];
      // empty shard, or shard fully at or below the resume position
      if (s.count === 0 || s.rangeEnd <= this.afterId + 1) this.shardIdx++;
      else break;
    }
  }

  private loadNextBlock(): boolean {
    this.releaseBlock();
    const shards = this.opened.header.shards;
    for (;;) {
      if (this.shardIdx >= shards.length) return false;
      const nBlocks = this.store.blockCountOf(this.opened.fileKey, this.shardIdx);
      if (this.blockIdx < nBlocks) break;
      this.shardIdx++;
      this.blockIdx = 0;
      this.skipEmptyShards();
    }
    const b = this.store.getBlock(this.opened.fileKey, this.shardIdx, this.blockIdx);
    this.session.pin(b.byteLength, 'decoded block ' + this.blockIdx + ' of shard ' + this.shardIdx + ' in set "' + this.opened.setId + '"');
    this.block = b.ids;
    this.blockBytes = b.byteLength;
    this.pos = 0;
    this.blockIdx++;
    return true;
  }

  private releaseBlock(): void {
    if (this.block) {
      this.session.unpin(this.blockBytes);
      this.block = null;
      this.blockBytes = 0;
    }
  }

  next(): number | null {
    for (;;) {
      if (this.done) return null;
      if (this.block === null || this.pos >= this.block.length) {
        if (!this.loadNextBlock()) {
          this.done = true;
          return null;
        }
      }
      const id = (this.block as Uint32Array)[this.pos++];
      if (id <= this.afterId) continue;
      return id;
    }
  }

  close(): void {
    this.releaseBlock();
    this.done = true;
  }
}

/** k-way merge with dedup: children must each be strictly increasing. */
class OrStream implements IdStream {
  private heads: (number | null)[];
  constructor(private readonly children: IdStream[]) {
    this.heads = children.map((c) => c.next());
  }

  next(): number | null {
    let min: number | null = null;
    for (const h of this.heads) {
      if (h !== null && (min === null || h < min)) min = h;
    }
    if (min === null) return null;
    for (let i = 0; i < this.children.length; i++) {
      if (this.heads[i] === min) this.heads[i] = this.children[i].next();
    }
    return min;
  }

  close(): void {
    this.children.forEach((c) => c.close());
  }
}

/** Intersection: children must each be strictly increasing. */
class AndStream implements IdStream {
  private heads: (number | null)[];
  constructor(private readonly children: IdStream[]) {
    if (children.length === 0) throw new IllegalArgumentError('and() requires at least one operand');
    this.heads = children.map((c) => c.next());
  }

  next(): number | null {
    for (;;) {
      let max: number | null = null;
      let allEqual = true;
      for (const h of this.heads) {
        if (h === null) return null;
        if (max === null || h > max) max = h;
      }
      for (const h of this.heads) {
        if (h !== max) {
          allEqual = false;
          break;
        }
      }
      if (allEqual) {
        const value = max as number;
        for (let i = 0; i < this.children.length; i++) this.heads[i] = this.children[i].next();
        return value;
      }
      for (let i = 0; i < this.children.length; i++) {
        let h = this.heads[i];
        while (h !== null && h < (max as number)) {
          h = this.children[i].next();
        }
        this.heads[i] = h;
        if (h === null) return null;
      }
    }
  }

  close(): void {
    this.children.forEach((c) => c.close());
  }
}

export interface QueryOptions {
  expr: Expr;
  /** setId -> file path for every set referenced by the expression */
  inputs: Record<string, string>;
  /** page size; required for the first page, must match the cursor when resuming */
  k?: number;
  cursor?: string;
  tenant?: string;
  quotaBytes: number;
}

export interface PageStats {
  tenant: string;
  quotaBytes: number;
  peakDecodedBytes: number;
  blocksDecoded: number;
  cacheHits: number;
  cacheMisses: number;
}

export interface PageResult {
  ids: number[];
  nextCursor: string | null;
  stats: PageStats;
}

export interface OpenedQuery {
  /** pull the next id, or null when the result is exhausted */
  next(): number | null;
  /** release all pinned decoded blocks; safe to call at any time (cancellation) */
  close(): void;
  readonly session: Session;
  readonly k: number;
  readonly exprHash: string;
  readonly resolvedInputs: [string, string][];
  readonly tenant: string;
}

export class QueryEngine {
  constructor(readonly store: SetStore) {}

  /**
   * Open a streaming result. The caller may stop pulling at any point and
   * call close(); resume state lives entirely in cursors, so cancelling and
   * re-reading with the same cursor neither loses nor duplicates ids.
   */
  openStream(opts: QueryOptions): OpenedQuery {
    const tenant = opts.tenant ?? 'default';
    if (opts.k !== undefined && (!Number.isInteger(opts.k) || opts.k < 1)) {
      throw new IllegalArgumentError('page size k must be an integer >= 1, got ' + opts.k);
    }
    const wanted = exprSets(opts.expr);
    const openedBySet = new Map<string, OpenedSet>();
    const resolvedInputs: [string, string][] = [];
    for (const setId of wanted) {
      const path = opts.inputs[setId];
      if (!path) throw new IllegalArgumentError('no input file provided for set "' + setId + '"');
      const opened = this.store.open(path);
      openedBySet.set(setId, opened);
      resolvedInputs.push([setId, opened.version]);
    }
    const hash = exprHash(opts.expr);

    let k: number;
    let lastId = -1;
    if (opts.cursor !== undefined) {
      const payload: CursorPayload = decodeCursor(opts.cursor);
      assertCursorBinding(payload, hash, resolvedInputs);
      if (opts.k !== undefined && opts.k !== payload.k) {
        throw new IllegalArgumentError('page size k=' + opts.k + ' does not match cursor k=' + payload.k);
      }
      k = payload.k;
      lastId = payload.last;
    } else {
      if (opts.k === undefined) throw new IllegalArgumentError('page size k is required for the first page');
      k = opts.k;
    }

    const session = new Session(tenant, opts.quotaBytes);
    const buildStream = (e: Expr): IdStream => {
      if (e.kind === 'set') {
        return new SetStream(this.store, openedBySet.get(e.set) as OpenedSet, session, lastId);
      }
      const kids = e.children.map(buildStream);
      return e.kind === 'and' ? new AndStream(kids) : new OrStream(kids);
    };

    const stream = buildStream(opts.expr);
    let closed = false;
    return {
      next(): number | null {
        if (closed) return null;
        return stream.next();
      },
      close(): void {
        if (!closed) {
          closed = true;
          stream.close();
        }
      },
      session,
      k,
      exprHash: hash,
      resolvedInputs,
      tenant,
    };
  }

  queryPage(opts: QueryOptions): PageResult {
    const hitsBefore = this.store.cache.hits;
    const missesBefore = this.store.cache.misses;
    const decodedBefore = this.store.blocksDecoded;

    const q = this.openStream(opts);
    const ids: number[] = [];
    let exhausted = false;
    try {
      // pull k+1 ids as a look-ahead: if the stream ends exactly on a page
      // boundary we still learn there is no next page and emit a null cursor
      while (ids.length < q.k + 1) {
        const id = q.next();
        if (id === null) {
          exhausted = true;
          break;
        }
        ids.push(id);
      }
    } finally {
      q.close();
    }
    if (!exhausted) {
      // drop the look-ahead id; resume is id-based so it is re-emitted by the next page
      ids.pop();
    }

    const nextCursor = exhausted
      ? null
      : encodeCursor({ v: 1, expr: q.exprHash, inputs: q.resolvedInputs, k: q.k, last: ids[ids.length - 1] });

    return {
      ids,
      nextCursor,
      stats: {
        tenant: q.tenant,
        quotaBytes: opts.quotaBytes,
        peakDecodedBytes: q.session.peakBytes,
        blocksDecoded: this.store.blocksDecoded - decodedBefore,
        cacheHits: this.store.cache.hits - hitsBefore,
        cacheMisses: this.store.cache.misses - missesBefore,
      },
    };
  }
}
