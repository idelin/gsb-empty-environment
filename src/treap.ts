/**
 * Persistent ordered map implemented as a treap (randomized BST) with
 * path copying. Every node carries a Merkle-style content hash so that
 * unchanged subtrees can be skipped wholesale during a diff.
 *
 * Priorities are a deterministic function of the key, so the same key set
 * always produces the same shape and two versions share every untouched
 * subtree by reference.
 */

export type Compare<K> = (a: K, b: K) => number;

export interface TreapConfig<K, V> {
  readonly compare: Compare<K>;
  /** Deterministic priority per key; must be stable across versions. */
  readonly keyPriority: (key: K) => number;
  /** Content digest of one node, fed with the digests of its children. */
  readonly hashNode: (key: K, value: V, leftHash: number, rightHash: number) => number;
}

export interface TreapNode<K, V> {
  readonly key: K;
  readonly value: V;
  readonly priority: number;
  readonly left: TreapNode<K, V> | null;
  readonly right: TreapNode<K, V> | null;
  /** Number of entries in this subtree. */
  readonly size: number;
  /** Smallest key in this subtree. */
  readonly lo: K;
  /** Largest key in this subtree. */
  readonly hi: K;
  /** Content digest covering key, value and both child digests. */
  readonly hash: number;
}

export function defaultCompare<K>(a: K, b: K): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

function fnv1a(str: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < str.length; i++) {
    h ^= str.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

function mix32(x: number): number {
  x = (x ^ (x >>> 16)) >>> 0;
  x = Math.imul(x, 0x7feb352d) >>> 0;
  x = (x ^ (x >>> 15)) >>> 0;
  x = Math.imul(x, 0x846ca68b) >>> 0;
  x = (x ^ (x >>> 16)) >>> 0;
  return x >>> 0;
}

export function defaultKeyPriority<K>(key: K): number {
  return mix32(fnv1a('prio:' + String(key)));
}

function stableStringify(x: unknown): string {
  try {
    const s = JSON.stringify(x);
    return s === undefined ? String(x) : s;
  } catch {
    return String(x);
  }
}

export function defaultHashNode<K, V>(key: K, value: V, leftHash: number, rightHash: number): number {
  return mix32(fnv1a(`node:${stableStringify(key)}:${stableStringify(value)}:${leftHash}:${rightHash}`));
}

export function makeConfig<K, V>(overrides: Partial<TreapConfig<K, V>> = {}): TreapConfig<K, V> {
  return {
    compare: overrides.compare ?? defaultCompare,
    keyPriority: overrides.keyPriority ?? defaultKeyPriority,
    hashNode: overrides.hashNode ?? defaultHashNode,
  };
}

export function sizeOf<K, V>(t: TreapNode<K, V> | null): number {
  return t === null ? 0 : t.size;
}

function makeNode<K, V>(
  cfg: TreapConfig<K, V>,
  key: K,
  value: V,
  left: TreapNode<K, V> | null,
  right: TreapNode<K, V> | null,
): TreapNode<K, V> {
  return {
    key,
    value,
    priority: cfg.keyPriority(key),
    left,
    right,
    size: 1 + sizeOf(left) + sizeOf(right),
    lo: left === null ? key : left.lo,
    hi: right === null ? key : right.hi,
    hash: cfg.hashNode(key, value, left === null ? 0 : left.hash, right === null ? 0 : right.hash),
  };
}

/**
 * Merges two treaps where every key in `l` is smaller than every key in `r`.
 * Reuses untouched spine nodes so sharing with the inputs is maximized.
 */
export function join<K, V>(
  cfg: TreapConfig<K, V>,
  l: TreapNode<K, V> | null,
  r: TreapNode<K, V> | null,
): TreapNode<K, V> | null {
  if (l === null) return r;
  if (r === null) return l;
  if (l.priority >= r.priority) {
    const newRight = join(cfg, l.right, r);
    return newRight === l.right ? l : makeNode(cfg, l.key, l.value, l.left, newRight);
  }
  const newLeft = join(cfg, l, r.left);
  return newLeft === r.left ? r : makeNode(cfg, r.key, r.value, newLeft, r.right);
}

export interface Split3<K, V> {
  /** Entries with key < k. */
  readonly less: TreapNode<K, V> | null;
  /** The node with key === k, or null when absent. */
  readonly equal: TreapNode<K, V> | null;
  /** Entries with key > k. */
  readonly greater: TreapNode<K, V> | null;
}

/** Splits a treap around key `k` without copying untouched subtrees. */
export function split3<K, V>(cfg: TreapConfig<K, V>, t: TreapNode<K, V> | null, k: K): Split3<K, V> {
  if (t === null) return { less: null, equal: null, greater: null };
  const c = cfg.compare(k, t.key);
  if (c === 0) return { less: t.left, equal: t, greater: t.right };
  if (c < 0) {
    const s = split3(cfg, t.left, k);
    return {
      less: s.less,
      equal: s.equal,
      greater: s.greater === t.left ? t : makeNode(cfg, t.key, t.value, s.greater, t.right),
    };
  }
  const s = split3(cfg, t.right, k);
  return {
    less: s.less === t.right ? t : makeNode(cfg, t.key, t.value, t.left, s.less),
    equal: s.equal,
    greater: s.greater,
  };
}

export function get<K, V>(cfg: TreapConfig<K, V>, t: TreapNode<K, V> | null, k: K): V | undefined {
  let cur = t;
  while (cur !== null) {
    const c = cfg.compare(k, cur.key);
    if (c === 0) return cur.value;
    cur = c < 0 ? cur.left : cur.right;
  }
  return undefined;
}

/** Returns a new root with `k` bound to `v`; returns `t` itself when nothing changes. */
export function set<K, V>(
  cfg: TreapConfig<K, V>,
  t: TreapNode<K, V> | null,
  k: K,
  v: V,
  valuesEqual: (a: V, b: V) => boolean = Object.is,
): TreapNode<K, V> | null {
  const s = split3(cfg, t, k);
  if (s.equal !== null && valuesEqual(s.equal.value, v)) return t;
  const leaf = makeNode(cfg, k, v, null, null);
  return join(cfg, s.less, join(cfg, leaf, s.greater));
}

/** Returns a new root without `k`; returns `t` itself when `k` is absent. */
export function remove<K, V>(
  cfg: TreapConfig<K, V>,
  t: TreapNode<K, V> | null,
  k: K,
): TreapNode<K, V> | null {
  const s = split3(cfg, t, k);
  if (s.equal === null) return t;
  return join(cfg, s.less, s.greater);
}

/** In-order iteration over all entries, ascending by key. */
export function* entries<K, V>(t: TreapNode<K, V> | null): Generator<readonly [K, V]> {
  if (t === null) return;
  yield* entries(t.left);
  yield [t.key, t.value] as const;
  yield* entries(t.right);
}
