/**
 * In-memory ordered table (MemTable) backed by a multi-level randomized
 * skip list.
 *
 * Concurrency model
 * -----------------
 * Node.js executes JavaScript on a single-threaded event loop, so every
 * forward-pointer swing during `insert` is an atomic, lock-free update with
 * respect to other tasks: no `await` ever happens in the middle of splicing
 * a node into the level indexes. Async writers and readers therefore
 * interleave only at well-defined yield points and never observe a
 * half-linked node.
 *
 * Snapshot isolation
 * ------------------
 * Every write is stamped with a monotonically increasing sequence number.
 * Each key node keeps a version chain of its values (newest first) and
 * nodes are never physically removed while the table lives. An iterator
 * captures the current sequence number at creation time and, while
 * traversing the immutable level-0 chain, only surfaces the newest version
 * whose seq <= its snapshot seq. Nodes and versions written *after* the
 * snapshot are invisible, so an iterator's result set is completely stable
 * regardless of concurrent inserts.
 */

export const MAX_LEVEL = 16;
export const BRANCHING_FACTOR = 0.5;

export type Comparator<K> = (a: K, b: K) => number;

export interface VersionedValue<V> {
  readonly seq: number;
  readonly value: V;
}

class SkipNode<K, V> {
  readonly key: K;
  readonly level: number;
  /** Versions of this key, newest (highest seq) first. Never mutated in place. */
  readonly versions: VersionedValue<V>[];
  /** Forward pointers; next[i] is the successor at level i. */
  readonly next: (SkipNode<K, V> | null)[];

  constructor(key: K, version: VersionedValue<V>, level: number) {
    this.key = key;
    this.level = level;
    this.versions = [version];
    this.next = new Array<SkipNode<K, V> | null>(level).fill(null);
  }

  /** Newest version visible at `snapshotSeq`, or null if none exists yet. */
  visibleVersion(snapshotSeq: number): VersionedValue<V> | null {
    for (const v of this.versions) {
      if (v.seq <= snapshotSeq) return v;
    }
    return null;
  }
}

export interface Entry<K, V> {
  readonly key: K;
  readonly value: V;
}

export interface RangeOptions<K> {
  /** Inclusive lower bound. Defaults to the smallest key. */
  gte?: K;
  /** Inclusive upper bound. Defaults to the largest key. */
  lte?: K;
  /** Explicit snapshot sequence number; defaults to the seq at creation. */
  snapshotSeq?: number;
}

const defaultComparator: Comparator<unknown> = (a, b) =>
  a < b ? -1 : a > b ? 1 : 0;

export class SkipList<K = number, V = unknown> {
  private head: SkipNode<K, V>;
  private level = 1;
  private seqCounter = 0;
  private keyCount = 0;
  private readonly compare: Comparator<K>;
  private readonly random: () => number;

  constructor(
    comparator?: Comparator<K>,
    random: () => number = Math.random,
  ) {
    this.compare = (comparator ?? defaultComparator) as Comparator<K>;
    this.random = random;
    // Sentinel head node; its key is never read.
    this.head = new SkipNode<K, V>(
      undefined as unknown as K,
      { seq: -1, value: undefined as unknown as V },
      MAX_LEVEL,
    );
  }

  /** Number of distinct keys currently in the table. */
  get size(): number {
    return this.keyCount;
  }

  /** Current write sequence number (i.e. the latest snapshot point). */
  get currentSeq(): number {
    return this.seqCounter;
  }

  private randomLevel(): number {
    let lvl = 1;
    while (lvl < MAX_LEVEL && this.random() < BRANCHING_FACTOR) lvl++;
    return lvl;
  }

  /**
   * Insert (or overwrite) a key/value pair. Returns the sequence number
   * assigned to this write.
   *
   * The predecessor search and the pointer swings below contain no `await`,
   * so each splice is atomic with respect to the event loop — a lock-free
   * update of the level indexes.
   */
  insert(key: K, value: V): number {
    const seq = ++this.seqCounter;
    const update = new Array<SkipNode<K, V>>(MAX_LEVEL);

    let cur = this.head;
    for (let i = this.level - 1; i >= 0; i--) {
      while (cur.next[i] !== null && this.compare(cur.next[i]!.key, key) < 0) {
        cur = cur.next[i]!;
      }
      update[i] = cur;
    }

    const existing = cur.next[0];
    if (existing !== null && this.compare(existing.key, key) === 0) {
      // Same key: push a new MVCC version instead of a duplicate node.
      existing.versions.unshift({ seq, value });
      return seq;
    }

    const lvl = this.randomLevel();
    if (lvl > this.level) {
      for (let i = this.level; i < lvl; i++) update[i] = this.head;
      this.level = lvl;
    }

    const node = new SkipNode<K, V>(key, { seq, value }, lvl);
    for (let i = 0; i < lvl; i++) {
      node.next[i] = update[i]!.next[i];
      update[i]!.next[i] = node; // atomic pointer swing
    }
    this.keyCount++;
    return seq;
  }

  /** Latest value for `key`, or undefined if absent. */
  get(key: K): V | undefined {
    let cur = this.head;
    for (let i = this.level - 1; i >= 0; i--) {
      while (cur.next[i] !== null && this.compare(cur.next[i]!.key, key) < 0) {
        cur = cur.next[i]!;
      }
    }
    const node = cur.next[0];
    if (node !== null && this.compare(node.key, key) === 0) {
      return node.versions[0]!.value;
    }
    return undefined;
  }

  /**
   * Create a read-only snapshot iterator over the (optionally bounded)
   * range. The snapshot sequence number is frozen at creation; later
   * inserts or overwrites are invisible to this iterator.
   */
  iterator(options: RangeOptions<K> = {}): IterableIterator<Entry<K, V>> {
    const snapshotSeq = options.snapshotSeq ?? this.seqCounter;
    const hasLower = options.gte !== undefined;
    const hasUpper = options.lte !== undefined;

    // Seek the first node >= gte using the level index.
    let cur = this.head;
    if (hasLower) {
      for (let i = this.level - 1; i >= 0; i--) {
        while (
          cur.next[i] !== null &&
          this.compare(cur.next[i]!.key, options.gte as K) < 0
        ) {
          cur = cur.next[i]!;
        }
      }
    }

    let node = cur.next[0];
    const self = this;

    function* gen(): IterableIterator<Entry<K, V>> {
      while (node !== null) {
        if (hasUpper && self.compare(node.key, options.lte as K) > 0) return;
        const visible = node.visibleVersion(snapshotSeq);
        if (visible !== null) {
          yield { key: node.key, value: visible.value };
        }
        node = node.next[0];
      }
    }

    return gen();
  }

  /** Convenience: materialize a full snapshot scan into an array. */
  entries(options: RangeOptions<K> = {}): Entry<K, V>[] {
    return [...this.iterator(options)];
  }
}
