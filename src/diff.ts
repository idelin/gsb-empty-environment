/**
 * Paginated, resumable diff between two persistent ordered-map roots.
 *
 * Cursor model
 * ------------
 * A cursor pins both version roots plus the last fully processed key
 * (an exclusive high-water mark). Everything with key <= lastKey is done;
 * resuming re-enters both trees at that mark and prunes whole subtrees
 * whose key range is already covered.
 *
 * Skipping and the explicit hash-trust model
 * ------------------------------------------
 * - Reference-equal subtrees are always skipped: identity cannot collide.
 * - With `trust: 'trusted'`, equal content digests are also skipped. This
 *   is fast but accepts the collision risk of the configured hashNode.
 * - With `trust: 'verified'` (default), equal digests are *not* trusted;
 *   the scan descends anyway and compares entry by entry, so a digest
 *   collision can never hide a real difference.
 *
 * Paging and cancellation
 * -----------------------
 * `pageSize` bounds the number of emitted diff items per page, never the
 * number of keys scanned. A page may therefore scan millions of shared
 * keys and emit nothing (an empty page is legal when cancellation hits).
 * Cancellation (via `shouldCancel` or `scanBudget`) unwinds immediately
 * and returns a cursor that resumes exactly where the scan stopped.
 */

import { split3 } from './treap.ts';
import type { TreapConfig, TreapNode } from './treap.ts';

export type DiffType = 'added' | 'deleted' | 'changed';

export interface DiffItem<K, V> {
  readonly type: DiffType;
  readonly key: K;
  /** Present for 'deleted' and 'changed'. */
  readonly oldValue?: V;
  /** Present for 'added' and 'changed'. */
  readonly newValue?: V;
}

export type HashTrust = 'trusted' | 'verified';

export interface DiffOptions<V> {
  /** How to treat equal subtree digests. Default: 'verified'. */
  readonly trust?: HashTrust;
  /** Value equality; default Object.is. */
  readonly valuesEqual?: (a: V, b: V) => boolean;
}

export interface DiffCursor<K, V> {
  readonly oldRoot: TreapNode<K, V> | null;
  readonly newRoot: TreapNode<K, V> | null;
  /** Exclusive high-water mark; null means nothing processed yet. */
  readonly lastKey: K | null;
}

export type PauseReason = 'page-full' | 'cancelled';

export interface DiffPage<K, V> {
  readonly items: DiffItem<K, V>[];
  /** Always a valid resume point, even when `done` is false. */
  readonly cursor: DiffCursor<K, V>;
  readonly done: boolean;
  readonly reason: 'complete' | PauseReason;
  readonly stats: { readonly scannedNodes: number };
}

export interface ScanControl {
  /** Polled at every visited node; returning true pauses the scan. */
  readonly shouldCancel?: () => boolean;
  /** Maximum number of tree nodes this page may visit before pausing. */
  readonly scanBudget?: number;
}

class Paused {
  readonly reason: PauseReason;
  constructor(reason: PauseReason) {
    this.reason = reason;
  }
}

interface ScanCtx<K, V> {
  items: DiffItem<K, V>[];
  limit: number;
  lo: K | null;
  highWater: K | null;
  scanned: number;
  budget: number;
  shouldCancel: (() => boolean) | undefined;
}

export class Differ<K, V> {
  private readonly cfg: TreapConfig<K, V>;
  private readonly trust: HashTrust;
  private readonly valuesEqual: (a: V, b: V) => boolean;

  constructor(cfg: TreapConfig<K, V>, options: DiffOptions<V> = {}) {
    this.cfg = cfg;
    this.trust = options.trust ?? 'verified';
    this.valuesEqual = options.valuesEqual ?? Object.is;
  }

  beginDiff(
    oldRoot: TreapNode<K, V> | null,
    newRoot: TreapNode<K, V> | null,
  ): DiffCursor<K, V> {
    return { oldRoot, newRoot, lastKey: null };
  }

  nextPage(cursor: DiffCursor<K, V>, pageSize: number, control: ScanControl = {}): DiffPage<K, V> {
    if (!Number.isInteger(pageSize) || pageSize < 1) {
      throw new RangeError('pageSize must be a positive integer');
    }
    const ctx: ScanCtx<K, V> = {
      items: [],
      limit: pageSize,
      lo: cursor.lastKey,
      highWater: cursor.lastKey,
      scanned: 0,
      budget: control.scanBudget ?? Number.POSITIVE_INFINITY,
      shouldCancel: control.shouldCancel,
    };
    let reason: 'complete' | PauseReason = 'complete';
    try {
      this.scan(cursor.oldRoot, cursor.newRoot, ctx);
    } catch (e) {
      if (e instanceof Paused) {
        reason = e.reason;
      } else {
        throw e;
      }
    }
    return {
      items: ctx.items,
      cursor: { oldRoot: cursor.oldRoot, newRoot: cursor.newRoot, lastKey: ctx.highWater },
      done: reason === 'complete',
      reason,
      stats: { scannedNodes: ctx.scanned },
    };
  }

  private visit(ctx: ScanCtx<K, V>): void {
    ctx.scanned += 1;
    if (ctx.scanned > ctx.budget || (ctx.shouldCancel !== undefined && ctx.shouldCancel())) {
      throw new Paused('cancelled');
    }
  }

  private emit(ctx: ScanCtx<K, V>, item: DiffItem<K, V>): void {
    if (ctx.items.length >= ctx.limit) throw new Paused('page-full');
    ctx.items.push(item);
    ctx.highWater = item.key;
  }

  private leqLo(key: K, ctx: ScanCtx<K, V>): boolean {
    return ctx.lo !== null && this.cfg.compare(key, ctx.lo) <= 0;
  }

  private scan(
    a: TreapNode<K, V> | null,
    b: TreapNode<K, V> | null,
    ctx: ScanCtx<K, V>,
  ): void {
    if (a === null && b === null) return;
    // Resume pruning: the whole aligned key range is already processed.
    if (ctx.lo !== null) {
      const rangeHi = this.rangeHi(a, b);
      if (rangeHi !== undefined && this.cfg.compare(rangeHi, ctx.lo) <= 0) return;
    }
    this.visit(ctx);
    if (a !== null && b !== null) {
      if (a === b) {
        // Structurally shared subtree: identical by construction.
        ctx.highWater = a.hi;
        return;
      }
      if (this.trust === 'trusted' && a.hash === b.hash) {
        // Digest equality trusted as content equality (collision risk accepted).
        ctx.highWater = a.hi;
        return;
      }
      // 'verified': equal digests still force a full descent below.
    }
    if (a === null) {
      this.emitAll(b as TreapNode<K, V>, 'added', ctx);
      return;
    }
    if (b === null) {
      this.emitAll(a, 'deleted', ctx);
      return;
    }
    // Align ranges by splitting b at a's root key, then recurse in order.
    const s = split3(this.cfg, b, a.key);
    this.scan(a.left, s.less, ctx);
    if (!this.leqLo(a.key, ctx)) {
      if (s.equal === null) {
        this.emit(ctx, { type: 'deleted', key: a.key, oldValue: a.value });
      } else if (!this.valuesEqual(a.value, s.equal.value)) {
        this.emit(ctx, { type: 'changed', key: a.key, oldValue: a.value, newValue: s.equal.value });
      } else {
        ctx.highWater = a.key;
      }
    }
    this.scan(a.right, s.greater, ctx);
  }

  private rangeHi(
    a: TreapNode<K, V> | null,
    b: TreapNode<K, V> | null,
  ): K | undefined {
    if (a === null) return b === null ? undefined : b.hi;
    if (b === null) return a.hi;
    return this.cfg.compare(a.hi, b.hi) >= 0 ? a.hi : b.hi;
  }

  private emitAll(t: TreapNode<K, V>, type: 'added' | 'deleted', ctx: ScanCtx<K, V>): void {
    if (ctx.lo !== null && this.cfg.compare(t.hi, ctx.lo) <= 0) return;
    this.visit(ctx);
    if (t.left !== null) this.emitAll(t.left, type, ctx);
    if (!this.leqLo(t.key, ctx)) {
      if (type === 'added') {
        this.emit(ctx, { type, key: t.key, newValue: t.value });
      } else {
        this.emit(ctx, { type, key: t.key, oldValue: t.value });
      }
    }
    if (t.right !== null) this.emitAll(t.right, type, ctx);
  }
}

/**
 * Reference implementation: a plain full merge join over two sorted entry
 * streams. Used to cross-check the paginated engine in tests.
 */
export function mergeDiff<K, V>(
  oldEntries: Iterable<readonly [K, V]>,
  newEntries: Iterable<readonly [K, V]>,
  compare: (a: K, b: K) => number,
  valuesEqual: (a: V, b: V) => boolean = Object.is,
): DiffItem<K, V>[] {
  const items: DiffItem<K, V>[] = [];
  const itA = oldEntries[Symbol.iterator]();
  const itB = newEntries[Symbol.iterator]();
  let x = itA.next();
  let y = itB.next();
  while (!x.done || !y.done) {
    if (y.done || (!x.done && compare(x.value[0], y.value[0]) < 0)) {
      items.push({ type: 'deleted', key: x.value[0], oldValue: x.value[1] });
      x = itA.next();
    } else if (x.done || compare(y.value[0], x.value[0]) < 0) {
      items.push({ type: 'added', key: y.value[0], newValue: y.value[1] });
      y = itB.next();
    } else {
      if (!valuesEqual(x.value[1], y.value[1])) {
        items.push({ type: 'changed', key: x.value[0], oldValue: x.value[1], newValue: y.value[1] });
      }
      x = itA.next();
      y = itB.next();
    }
  }
  return items;
}
