/**
 * MVCC + OCC transactional key-value store.
 *
 * - Each key keeps a sorted list of immutable versions (a "version chain").
 * - Every transaction begins on a fixed snapshot: beginReadTs = latest commit ts.
 *   Readers therefore never observe uncommitted/intermediate writes.
 * - Writers buffer their changes in memory. At commit time the read set is
 *   validated against versions created after the snapshot (first-committer-wins).
 * - On conflict the commit is rejected and all buffered writes are discarded
 *   atomically (nothing is ever partially visible).
 * - Committed changes are appended to an append-only operation log; a store can
 *   be rebuilt byte-for-byte by replaying that log.
 * - Garbage collection removes versions that no active (or future) reader can
 *   see, always retaining at least the latest committed version of every key.
 *
 * The store is in-memory. Node is single-threaded, but transactions commit
 * through `await` boundaries, so commit validation+application is serialized
 * with a promise-chain mutex.
 */

/** A committed (or tombstone) version of a key. */
export interface Version<V = unknown> {
  /** Commit timestamp that made this version visible. */
  ts: number;
  /** Transaction id that created it. */
  txnId: number;
  /** Value, or undefined when this version is a delete tombstone. */
  value: V | undefined;
  /** True when the version represents a deletion. */
  deleted: boolean;
}

/** One durable commit record. Replaying these records reconstructs the store. */
export interface LogRecord<V = unknown> {
  commitTs: number;
  txnId: number;
  /** All keys written by this transaction, each mapped to its value/tombstone. */
  writes: Array<{ key: string; value: V | undefined; deleted: boolean }>;
}

export class TransactionAbortedError extends Error {
  readonly reason: string;
  constructor(reason: string) {
    super(`transaction aborted: ${reason}`);
    this.name = 'TransactionAbortedError';
    this.reason = reason;
  }
}

export type IsolationLevel = 'snapshot' | 'read-only';
export type TxnState = 'active' | 'committed' | 'aborted';

export interface RunOptions {
  /** Max attempts including the first one (default 1 = no retry). */
  retries?: number;
  readOnly?: boolean;
}

/**
 * A single transaction handle. Obtain one with `store.begin()` (read-write) or
 * `store.beginReadOnly()` (snapshot reader).
 */
export class Txn<V = unknown> {
  /** Fixed snapshot timestamp captured at begin(). */
  readonly readTs: number;
  readonly id: number;
  readonly readOnly: boolean;

  /** @internal */
  state: TxnState = 'active';
  /** @internal keys read against the snapshot (exists-or-not is tracked). */
  readonly readSet: Set<string> = new Set();
  /** @internal buffered writes: key -> {value, deleted}. */
  readonly writeSet: Map<string, { value: V | undefined; deleted: boolean }> =
    new Map();

  /** @internal store reference (name-stable internal collaborator). */
  private readonly store: MVCCStore<V>;

  /** @internal */
  constructor(store: MVCCStore<V>, id: number, readTs: number, readOnly: boolean) {
    this.store = store;
    this.id = id;
    this.readTs = readTs;
    this.readOnly = readOnly;
  }

  /**
   * Read a key on the transaction's fixed snapshot plus its own buffered writes.
   */
  get(key: string): V | undefined {
    this.ensureActive();
    this.readSet.add(key);
    const own = this.writeSet.get(key);
    if (own) return own.deleted ? undefined : own.value;
    const versions = this.store.versionsForRead(key);
    const v = findVisible(versions, this.readTs);
    return v && !v.deleted ? v.value : undefined;
  }

  /** Like get(), but also reports whether the key exists in the snapshot. */
  has(key: string): boolean {
    this.ensureActive();
    this.readSet.add(key);
    if (this.writeSet.has(key)) {
      const own = this.writeSet.get(key)!;
      return !own.deleted;
    }
    const v = findVisible(this.store.versionsForRead(key), this.readTs);
    return v !== undefined && !v.deleted;
  }

  set(key: string, value: V): void {
    this.ensureWritable();
    this.writeSet.set(key, { value, deleted: false });
  }

  delete(key: string): void {
    this.ensureWritable();
    this.writeSet.set(key, { value: undefined, deleted: true });
  }

  /** Buffer size of pending (uncommitted) writes. */
  get pendingWriteCount(): number {
    return this.writeSet.size;
  }

  /** Validate the read set, then install all buffered writes atomically. */
  commit(): Promise<void> {
    if (this.state !== 'active') {
      return Promise.reject(
        new TransactionAbortedError(`txn #${this.id} is ${this.state}`),
      );
    }
    return this.store.commitTxn(this);
  }

  /** Discard all buffered writes. Never touches committed state. */
  rollback(): void {
    if (this.state !== 'active') return;
    this.state = 'aborted';
    this.writeSet.clear();
    this.store.txnEnded(this);
  }

  private ensureActive(): void {
    if (this.state !== 'active') {
      throw new TransactionAbortedError(`txn #${this.id} is ${this.state}`);
    }
  }

  private ensureWritable(): void {
    this.ensureActive();
    if (this.readOnly) {
      throw new TransactionAbortedError(`txn #${this.id} is read-only`);
    }
  }
}

/** Return the newest version visible at a read timestamp (versions sorted asc). */
function findVisible<V>(
  versions: Array<Version<V>> | undefined,
  readTs: number,
): Version<V> | undefined {
  if (!versions || versions.length === 0) return undefined;
  let lo = 0;
  let hi = versions.length;
  // Upper-bound search: first index with ts > readTs.
  while (lo < hi) {
    const mid = (lo + hi) >>> 1;
    if (versions[mid].ts <= readTs) lo = mid + 1;
    else hi = mid;
  }
  return lo > 0 ? versions[lo - 1] : undefined;
}

export interface GCStats {
  keysRemoved: number;
  versionsRemoved: number;
  /** Oldest snapshot timestamp that versions must remain visible for. */
  floorReadTs: number;
}

export class MVCCStore<V = unknown> {
  private data: Map<string, Array<Version<V>>> = new Map();
  private log: Array<LogRecord<V>> = [];
  private active: Map<number, Txn<V>> = new Map();
  private nextTxnId = 1;
  private commitTs = 0;
  private mutex: Promise<void> = Promise.resolve();

  begin(): Txn<V> {
    return this.startTxn(false);
  }

  beginReadOnly(): Txn<V> {
    return this.startTxn(true);
  }

  private startTxn(readOnly: boolean): Txn<V> {
    const txn = new Txn<V>(this, this.nextTxnId++, this.commitTs, readOnly);
    this.active.set(txn.id, txn);
    return txn;
  }

  /** Latest commit timestamp (= the newest available snapshot). */
  get currentTs(): number {
    return this.commitTs;
  }

  /** @internal */
  versionsForRead(key: string): Array<Version<V>> | undefined {
    return this.data.get(key);
  }

  /** @internal */
  txnEnded(txn: Txn<V>): void {
    this.active.delete(txn.id);
  }

  /** @internal Serialize validation+application of one transaction. */
  commitTxn(txn: Txn<V>): Promise<void> {
    if (txn.readOnly || txn.pendingWriteCount === 0) {
      txn.state = 'committed';
      this.txnEnded(txn);
      return Promise.resolve();
    }
    const run = this.mutex.then(() => this.validateAndApply(txn));
    // Keep the queue alive even if a prior commit rejected.
    this.mutex = run.then(
      () => undefined,
      () => undefined,
    );
    return run;
  }

  private validateAndApply(txn: Txn<V>): void {
    for (const key of txn.readSet) {
      const chain = this.data.get(key);
      if (chain && chain[chain.length - 1].ts > txn.readTs) {
        this.reject(txn, `read-write conflict on key "${key}"`);
      }
    }
    // Blind writes participate in conflict detection too, so two writers can
    // never silently overwrite one another even without a prior read.
    for (const key of txn.writeSet.keys()) {
      if (txn.readSet.has(key)) continue;
      const chain = this.data.get(key);
      if (chain && chain[chain.length - 1].ts > txn.readTs) {
        this.reject(txn, `write-write conflict on key "${key}"`);
      }
    }

    const newTs = this.commitTs + 1;
    const record: LogRecord<V> = { commitTs: newTs, txnId: txn.id, writes: [] };
    for (const [key, change] of txn.writeSet) {
      let chain = this.data.get(key);
      if (!chain) {
        chain = [];
        this.data.set(key, chain);
      }
      const version: Version<V> = {
        ts: newTs,
        txnId: txn.id,
        value: change.deleted ? undefined : change.value,
        deleted: change.deleted,
      };
      chain.push(version);
      record.writes.push({
        key,
        value: change.deleted ? undefined : change.value,
        deleted: change.deleted,
      });
    }
    this.commitTs = newTs;
    this.log.push(record);
    txn.writeSet.clear();
    txn.state = 'committed';
    this.txnEnded(txn);
  }

  private reject(txn: Txn<V>, reason: string): never {
    txn.writeSet.clear();
    txn.state = 'aborted';
    this.txnEnded(txn);
    throw new TransactionAbortedError(reason);
  }

  /** Number of commit records appended so far. */
  get logLength(): number {
    return this.log.length;
  }

  /** A copy of the append-only commit log, in commit order. */
  getLog(): Array<LogRecord<V>> {
    return this.log.map((r) => ({
      commitTs: r.commitTs,
      txnId: r.txnId,
      writes: r.writes.map((w) => ({ ...w })),
    }));
  }

  /**
   * Rebuild a store by replaying a commit log. The same log always yields the
   * same state; version timestamps come from the records, not replay order.
   */
  static replay<V>(records: Array<LogRecord<V>>): MVCCStore<V> {
    const store = new MVCCStore<V>();
    const sorted = [...records].sort((a, b) => a.commitTs - b.commitTs);
    for (const record of sorted) {
      for (const w of record.writes) {
        let chain = store.data.get(w.key);
        if (!chain) {
          chain = [];
          store.data.set(w.key, chain);
        }
        chain.push({
          ts: record.commitTs,
          txnId: record.txnId,
          value: w.deleted ? undefined : w.value,
          deleted: w.deleted,
        });
      }
      store.commitTs = Math.max(store.commitTs, record.commitTs);
      store.log.push({
        commitTs: record.commitTs,
        txnId: record.txnId,
        writes: record.writes.map((w) => ({ ...w })),
      });
    }
    return store;
  }

  /**
   * Remove versions that no active reader can ever observe.
   *
   * A version with timestamp > floorReadTs is retained because the oldest
   * active snapshot may still need it; among versions <= floorReadTs only the
   * newest is retained (it is what that snapshot actually sees). Keys are only
   * dropped when every retained version is <= floor and the newest is a
   * tombstone (no active or future reader can observe a live value).
   */
  garbageCollect(): GCStats {
    let floor = Infinity;
    for (const txn of this.active.values()) {
      if (txn.state === 'active' && txn.readTs < floor) floor = txn.readTs;
    }
    const floorReadTs = floor === Infinity ? this.commitTs : floor;

    let versionsRemoved = 0;
    const emptyKeys: string[] = [];
    for (const [key, chain] of this.data) {
      // `cut` = number of versions with ts <= floorReadTs. Retain the newest
      // one of those (the version the oldest snapshot actually reads) plus
      // every version newer than the snapshot.
      let cut = 0;
      for (let i = 0; i < chain.length; i++) {
        if (chain[i].ts <= floorReadTs) cut = i + 1;
      }
      const kept = cut > 0 ? chain.slice(cut - 1) : chain.slice();
      versionsRemoved += Math.max(0, cut - 1);
      const allCovered = cut === chain.length;
      if (allCovered && kept[kept.length - 1].deleted) {
        emptyKeys.push(key);
      } else {
        this.data.set(key, kept);
      }
    }
    for (const key of emptyKeys) this.data.delete(key);
    return { keysRemoved: emptyKeys.length, versionsRemoved, floorReadTs };
  }

  /** Snapshot number of stored versions (mostly for tests/diagnostics). */
  versionCount(key?: string): number {
    if (key !== undefined) return this.data.get(key)?.length ?? 0;
    let total = 0;
    for (const chain of this.data.values()) total += chain.length;
    return total;
  }

  /** Number of currently open transactions. */
  get activeTxnCount(): number {
    return this.active.size;
  }
}

/**
 * Run `fn` inside a transaction, committing afterwards. On a validation
 * conflict the function is replayed against a fresh snapshot, up to
 * `retries + 1` attempts. Other aborts propagate immediately.
 */
export async function runTransaction<V, T>(
  store: MVCCStore<V>,
  fn: (txn: Txn<V>) => T | Promise<T>,
  options: RunOptions = {},
): Promise<T> {
  const maxAttempts = Math.max(1, (options.retries ?? 0) + 1);
  for (let attempt = 1; attempt <= maxAttempts; attempt++) {
    const txn = options.readOnly ? store.beginReadOnly() : store.begin();
    try {
      const result = await fn(txn);
      await txn.commit();
      return result;
    } catch (err) {
      if (
        err instanceof TransactionAbortedError &&
        /conflict/.test(err.reason) &&
        attempt < maxAttempts
      ) {
        continue;
      }
      if (txn.state === 'active') txn.rollback();
      throw err;
    }
  }
  /* c8 ignore next 2 */
  throw new TransactionAbortedError('exhausted retries');
}
