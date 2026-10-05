/**
 * Paginated diff between two versions of a persistent ordered map.
 *
 * Emits `added` / `removed` / `changed` items in ascending key order.
 * A page contains at most `pageSize` *output items*; the number of keys
 * scanned to produce a page is unbounded (identical regions are skipped
 * or compared without emitting anything).
 *
 * Resumption: a `DiffCursor` binds both version handles plus the last
 * processed key. Re-seeking after a key costs O(log n) per side, so a
 * cancelled or completed page can be resumed cheaply at any time.
 *
 * Hash-trust model (explicit, per diff):
 * - reference-identical subtrees are always skipped (same object, same
 *   content — no trust required);
 * - `'verified'` (default): equal hashes only take the fast path when the
 *   nodes are also reference-identical; otherwise the engine descends and
 *   compares element-wise, so a hash collision can never hide a real
 *   difference. Skipping still happens for shared structure;
 * - `'trusted'`: equal hashes are treated as proof of equality and the
 *   subtree pair is skipped without inspection. The caller asserts the
 *   hash is collision-resistant for their data. A collision can then
 *   silently mask differences — that is the documented trade-off.
 */

import type { CompareFn, PMap, PNode } from './avl.ts';
import type { Version, VersionStore } from './store.ts';

export type DiffItem<K, V> =
  | { readonly type: 'added'; readonly key: K; readonly value: V }
  | { readonly type: 'removed'; readonly key: K; readonly value: V }
  | {
      readonly type: 'changed';
      readonly key: K;
      readonly oldValue: V;
      readonly newValue: V;
    };

export type HashTrust = 'verified' | 'trusted';

/** Structural subset of `AbortSignal` (avoids a hard DOM/@types/node dep). */
export interface CancelSignal {
  readonly aborted: boolean;
}

export interface DiffOptions<K, V> {
  /** Max output items per page. Default 1024. */
  pageSize?: number;
  /** Hash-trust model for subtree summaries. Default `'verified'`. */
  hashTrust?: HashTrust;
  /** Value equality for `changed` detection. Default `Object.is`. */
  valueEquals?: (a: V, b: V) => boolean;
  /** Cancellation. An aborted page returns early with `cancelled: true`
   *  and a cursor that resumes exactly where it stopped. */
  signal?: CancelSignal;
}

export interface DiffCursor<K> {
  readonly left: Version;
  readonly right: Version;
  /** Last processed key; `null` means "from the beginning". */
  readonly lastKey: K | null;
}

export interface DiffPage<K, V> {
  readonly items: DiffItem<K, V>[];
  /** Resume token for the next page. Valid whether or not `done`. */
  readonly cursor: DiffCursor<K>;
  /** True when both maps were consumed to the end. */
  readonly done: boolean;
  /** True when the page was cut short by `signal`. */
  readonly cancelled: boolean;
  /** Keys compared one-by-one during this page (both maps summed). */
  readonly scannedKeys: number;
  /** Keys skipped inside identical subtrees (both maps summed). */
  readonly skippedKeys: number;
}

/** Open a diff between two versions; feed the cursor to `diffPage`. */
export function openDiff<K>(left: Version, right: Version): DiffCursor<K> {
  return { left, right, lastKey: null };
}

/**
 * Traversal frontier for one map.
 *
 * Invariant: the remaining key sequence is `inorder(pending)` followed by
 * the entries of `path` from top to bottom (each `path` node's left
 * subtree already consumed).
 */
class Frontier<K, V> {
  readonly path: PNode<K, V>[] = [];
  pending: PNode<K, V> | null = null;

  static after<K, V>(
    root: PNode<K, V> | null,
    lastKey: K | null,
    compare: CompareFn<K>,
  ): Frontier<K, V> {
    const frontier = new Frontier<K, V>();
    if (lastKey === null) {
      frontier.pending = root;
      return frontier;
    }
    // Seek: keep only nodes whose key is strictly greater than `lastKey`.
    let node = root;
    while (node) {
      if (compare(lastKey, node.key) < 0) {
        frontier.path.push(node);
        node = node.left;
      } else {
        node = node.right;
      }
    }
    return frontier;
  }

  /** Move one level down the left spine of `pending`. */
  descendOne(): void {
    const node = this.pending!;
    this.path.push(node);
    this.pending = node.left;
  }

  /** Next unconsumed entry node, or null when exhausted. */
  top(): PNode<K, V> | null {
    const path = this.path;
    return path.length > 0 ? path[path.length - 1] : null;
  }

  /** Consume the top entry; its right subtree becomes pending. */
  advance(): void {
    const node = this.path.pop()!;
    this.pending = node.right;
  }
}

export function diffPage<K, V>(
  store: VersionStore<K, V>,
  cursor: DiffCursor<K>,
  options: DiffOptions<K, V> = {},
): DiffPage<K, V> {
  const pageSize = options.pageSize ?? 1024;
  if (!(pageSize >= 1)) {
    throw new RangeError('pmap-diff: pageSize must be >= 1');
  }
  const trust = options.hashTrust ?? 'verified';
  const valueEquals = options.valueEquals ?? Object.is;
  const signal = options.signal;

  // Resolve both versions up front: a released version fails the page
  // before any item is emitted.
  const leftMap: PMap<K, V> = store.resolve(cursor.left);
  const rightMap: PMap<K, V> = store.resolve(cursor.right);
  const compare = leftMap.compare;

  const left = Frontier.after(leftMap.root, cursor.lastKey, compare);
  const right = Frontier.after(rightMap.root, cursor.lastKey, compare);

  const items: DiffItem<K, V>[] = [];
  let scannedKeys = 0;
  let skippedKeys = 0;
  let lastKey = cursor.lastKey;
  let done = false;
  let cancelled = false;

  while (items.length < pageSize) {
    if (signal?.aborted) {
      cancelled = true;
      break;
    }

    // Materialize/skip phase: align both frontiers so their next entries
    // are on top of `path`, skipping identical subtrees wholesale.
    for (;;) {
      const lp = left.pending;
      const rp = right.pending;
      if (lp && rp) {
        if (lp === rp || (trust === 'trusted' && lp.hash === rp.hash)) {
          skippedKeys += lp.size + rp.size;
          left.pending = null;
          right.pending = null;
          continue;
        }
        left.descendOne();
        right.descendOne();
        continue;
      }
      if (lp) {
        left.descendOne();
        continue;
      }
      if (rp) {
        right.descendOne();
        continue;
      }
      break;
    }

    const ln = left.top();
    const rn = right.top();
    if (!ln && !rn) {
      done = true;
      break;
    }
    if (!rn) {
      items.push({ type: 'removed', key: ln!.key, value: ln!.value });
      lastKey = ln!.key;
      left.advance();
      scannedKeys += 1;
      continue;
    }
    if (!ln) {
      items.push({ type: 'added', key: rn.key, value: rn.value });
      lastKey = rn.key;
      right.advance();
      scannedKeys += 1;
      continue;
    }
    const cmp = compare(ln.key, rn.key);
    if (cmp < 0) {
      items.push({ type: 'removed', key: ln.key, value: ln.value });
      lastKey = ln.key;
      left.advance();
      scannedKeys += 1;
    } else if (cmp > 0) {
      items.push({ type: 'added', key: rn.key, value: rn.value });
      lastKey = rn.key;
      right.advance();
      scannedKeys += 1;
    } else {
      if (!valueEquals(ln.value, rn.value)) {
        items.push({
          type: 'changed',
          key: ln.key,
          oldValue: ln.value,
          newValue: rn.value,
        });
      }
      lastKey = ln.key;
      left.advance();
      right.advance();
      scannedKeys += 2;
    }
  }

  return {
    items,
    cursor: { left: cursor.left, right: cursor.right, lastKey },
    done,
    cancelled,
    scannedKeys,
    skippedKeys,
  };
}
