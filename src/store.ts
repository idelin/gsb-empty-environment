/**
 * TransactionalStore — a transactional, versioned, branchable state container
 * over immutable JSON trees.
 *
 * Core guarantees:
 * - One committed transaction produces exactly one global version.
 * - Subscribers are notified at most once per version and always observe the
 *   complete state tree of that version (never a per-slice mix).
 * - Commits issued from inside a notification callback are applied
 *   immediately, but their notifications are queued until the current
 *   notification round has finished (FIFO).
 * - A throwing listener never blocks the remaining listeners; the error is
 *   recorded in `listenerErrors` and forwarded to `onListenerError`.
 * - checkout(v) reads history; begin({ base: v }) forks a new branch from v.
 *   Existing commits are immutable and are never overwritten.
 * - Memory is metered per retained tree node (see Arena). Versions that are
 *   neither head, nor pinned by an active snapshot, nor pending notification
 *   are evicted oldest-first when the retained weight exceeds `capacity`.
 */

import { Arena } from './arena.ts';
import { assertJsonValue, cloneJson, isContainer } from './json.ts';
import type { JsonObject, JsonValue, Path } from './json.ts';

export type Listener = (state: JsonObject, version: number) => void;

export interface StoreOptions {
  /** Soft cap on retained node weight; oldest evictable history is reclaimed. */
  capacity?: number;
  onListenerError?: (error: unknown, version: number) => void;
}

export interface CheckoutView {
  version: number;
  branch: number;
  parent: number | null;
  state: JsonObject;
}

export interface Snapshot {
  readonly version: number;
  readonly state: JsonObject;
  release(): void;
}

export interface Ledger {
  capacity: number;
  retained: number;
  allocatedTotal: number;
  freedTotal: number;
  liveCommits: number;
  evictedCommits: number;
  pinnedVersions: number[];
  headVersion: number;
  lastVersion: number;
}

type TxOp =
  | { kind: 'set'; path: Path; value: JsonValue }
  | { kind: 'delete'; path: Path };

interface CommitRecord {
  version: number;
  branch: number;
  parent: number | null;
  root: JsonObject;
  evicted: boolean;
}

function assertPath(path: Path): void {
  if (!Array.isArray(path) || path.length === 0) {
    throw new TypeError('path must be a non-empty array of string/number segments');
  }
  for (const segment of path) {
    const ok =
      typeof segment === 'string' ||
      (typeof segment === 'number' && Number.isInteger(segment) && segment >= 0);
    if (!ok) throw new TypeError(`invalid path segment: ${String(segment)}`);
  }
}

function emptyContainerFor(key: string | number): JsonObject | JsonValue[] {
  return typeof key === 'number' ? [] : {};
}

/** Pure persistent set: returns a new tree, path-copying only what changed. */
function pset(
  node: JsonValue | undefined,
  path: Path,
  index: number,
  value: JsonValue,
): JsonValue {
  if (index === path.length) return value;
  const key = path[index];
  if (node === undefined) node = emptyContainerFor(key);
  if (Array.isArray(node)) {
    if (typeof key !== 'number') {
      throw new TypeError(`array requires a numeric index (segment ${index})`);
    }
    if (key > node.length) {
      throw new RangeError(`array index ${key} out of bounds (length ${node.length})`);
    }
    const clone = node.slice();
    clone[key] = pset(node[key], path, index + 1, value);
    return clone;
  }
  if (isContainer(node)) {
    if (typeof key !== 'string') {
      throw new TypeError(`object requires a string key (segment ${index})`);
    }
    const clone: JsonObject = { ...(node as JsonObject) };
    clone[key] = pset(node[key], path, index + 1, value);
    return clone;
  }
  throw new TypeError(`path traverses a scalar (segment ${index})`);
}

/** Pure persistent delete: object keys are removed, array indices are spliced. */
function pdel(node: JsonValue, path: Path, index: number): JsonValue {
  const key = path[index];
  if (!isContainer(node)) {
    throw new TypeError(`path traverses a scalar (segment ${index})`);
  }
  const last = index === path.length - 1;
  if (Array.isArray(node)) {
    if (typeof key !== 'number' || key >= node.length) {
      throw new RangeError(`array index ${String(key)} out of bounds (length ${node.length})`);
    }
    const clone = node.slice();
    if (last) {
      clone.splice(key, 1);
    } else {
      clone[key] = pdel(node[key], path, index + 1);
    }
    return clone;
  }
  if (typeof key !== 'string') {
    throw new TypeError(`object requires a string key (segment ${index})`);
  }
  if (!Object.prototype.hasOwnProperty.call(node, key)) {
    throw new RangeError(`path not found: ${key}`);
  }
  const clone: JsonObject = { ...(node as JsonObject) };
  if (last) {
    delete clone[key];
  } else {
    clone[key] = pdel(node[key], path, index + 1);
  }
  return clone;
}

export class Transaction {
  private store: TransactionalStore;
  readonly base: number;
  private ops: TxOp[] = [];
  private finished = false;

  constructor(store: TransactionalStore, base: number) {
    this.store = store;
    this.base = base;
  }

  set(path: Path, value: JsonValue): this {
    this.assertOpen();
    assertPath(path);
    assertJsonValue(value, 'set value');
    this.ops.push({ kind: 'set', path: [...path], value: cloneJson(value) });
    return this;
  }

  delete(path: Path): this {
    this.assertOpen();
    assertPath(path);
    this.ops.push({ kind: 'delete', path: [...path] });
    return this;
  }

  /** Applies all ops atomically, producing one new global version. */
  commit(): number {
    this.assertOpen();
    this.finished = true;
    return this.store.commitTransaction(this.base, this.ops);
  }

  private assertOpen(): void {
    if (this.finished) throw new Error('transaction is already committed');
  }
}

export class TransactionalStore {
  private arena = new Arena();
  private commits = new Map<number, CommitRecord>();
  private listeners = new Set<Listener>();
  private notifyQueue: number[] = [];
  private pendingNotify = new Set<number>();
  private notifying = false;
  private pins = new Map<number, number>();
  private lastVersion: number;
  private headVersion: number;
  private headBranch: number;
  private lastBranch = 0;
  private evictedCount = 0;
  private capacity: number;
  private onListenerError?: (error: unknown, version: number) => void;
  readonly listenerErrors: Array<{ version: number; error: unknown }> = [];

  constructor(initial: JsonObject, options: StoreOptions = {}) {
    assertJsonValue(initial, 'initial state');
    if (!isContainer(initial) || Array.isArray(initial)) {
      throw new TypeError('initial state must be a JSON object');
    }
    this.capacity = options.capacity ?? Number.POSITIVE_INFINITY;
    if (!(this.capacity >= 0)) throw new RangeError('capacity must be >= 0');
    this.onListenerError = options.onListenerError;
    const root = cloneJson(initial) as JsonObject;
    this.link(root);
    this.arena.retain(root);
    this.commits.set(0, { version: 0, branch: 0, parent: null, root, evicted: false });
    this.lastVersion = 0;
    this.headVersion = 0;
    this.headBranch = 0;
  }

  /** Current head version. */
  get version(): number {
    return this.headVersion;
  }

  /** Complete immutable state tree at head. */
  get state(): JsonObject {
    return this.commits.get(this.headVersion)!.root;
  }

  begin(options: { base?: number } = {}): Transaction {
    const base = options.base ?? this.headVersion;
    const record = this.commits.get(base);
    if (!record || record.evicted) {
      throw new RangeError(`base version ${base} is not available (unknown or reclaimed)`);
    }
    return new Transaction(this, base);
  }

  /** @internal Called by Transaction.commit(). */
  commitTransaction(base: number, ops: TxOp[]): number {
    const record = this.commits.get(base);
    if (!record || record.evicted) {
      throw new RangeError(`base version ${base} was reclaimed before commit`);
    }
    let root: JsonObject = record.root;
    for (const op of ops) {
      root = (op.kind === 'set'
        ? pset(root, op.path, 0, op.value)
        : pdel(root, op.path, 0)) as JsonObject;
    }
    this.link(root);
    const version = this.lastVersion + 1;
    const branch = base === this.headVersion ? this.headBranch : ++this.lastBranch;
    this.lastVersion = version;
    this.commits.set(version, { version, branch, parent: base, root, evicted: false });
    this.arena.retain(root);
    this.headVersion = version;
    this.headBranch = branch;
    this.gc();
    this.enqueueNotification(version);
    return version;
  }

  /**
   * Subscribe to commit notifications. Returns an unsubscribe function that
   * takes effect immediately (even mid-round). Each listener is invoked at
   * most once per version, in version order, with the complete state tree.
   */
  subscribe(listener: Listener): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  /** Read-only view of a historical version; throws if it was reclaimed. */
  checkout(version: number): CheckoutView {
    const record = this.commits.get(version);
    if (!record) throw new RangeError(`unknown version ${version}`);
    if (record.evicted) {
      throw new RangeError(`version ${version} has been reclaimed (capacity ${this.capacity})`);
    }
    return { version, branch: record.branch, parent: record.parent, state: record.root };
  }

  /**
   * Pin a version so its tree nodes can never be reclaimed. release() drops
   * the pin (idempotent) and triggers garbage collection.
   */
  snapshot(version: number = this.headVersion): Snapshot {
    const record = this.commits.get(version);
    if (!record || record.evicted) {
      throw new RangeError(`version ${version} is not available (unknown or reclaimed)`);
    }
    this.arena.retain(record.root);
    this.pins.set(version, (this.pins.get(version) ?? 0) + 1);
    let released = false;
    return {
      version,
      get state(): JsonObject {
        if (released) throw new Error('snapshot has been released');
        return record.root;
      },
      release: () => {
        if (released) return;
        released = true;
        const remaining = this.pins.get(version)! - 1;
        if (remaining === 0) this.pins.delete(version);
        else this.pins.set(version, remaining);
        this.arena.release(record.root);
        this.gc();
      },
    };
  }

  hasVersion(version: number): boolean {
    return this.commits.has(version);
  }

  isEvicted(version: number): boolean {
    return this.commits.get(version)?.evicted ?? false;
  }

  /** Memory ledger snapshot. Invariant: retained === allocatedTotal - freedTotal. */
  ledger(): Ledger {
    let liveCommits = 0;
    for (const record of this.commits.values()) {
      if (!record.evicted) liveCommits += 1;
    }
    return {
      capacity: this.capacity,
      retained: this.arena.retained,
      allocatedTotal: this.arena.allocatedTotal,
      freedTotal: this.arena.freedTotal,
      liveCommits,
      evictedCommits: this.evictedCount,
      pinnedVersions: Array.from(this.pins.keys()).sort((a, b) => a - b),
      headVersion: this.headVersion,
      lastVersion: this.lastVersion,
    };
  }

  /**
   * Roots currently retained by the store (every non-evicted commit; active
   * snapshots always pin a non-evicted commit). Exposed for ledger audits.
   */
  retainedRoots(): JsonObject[] {
    const roots: JsonObject[] = [];
    for (const record of this.commits.values()) {
      if (!record.evicted) roots.push(record.root);
    }
    return roots;
  }

  /** Register newly built nodes and their parent->child references. */
  private link(node: JsonValue): void {
    if (!isContainer(node) || this.arena.has(node)) return;
    for (const child of Object.values(node)) this.link(child as JsonValue);
    this.arena.alloc(node);
    for (const child of Object.values(node)) this.arena.retainValue(child as JsonValue);
  }

  private enqueueNotification(version: number): void {
    this.notifyQueue.push(version);
    this.pendingNotify.add(version);
    if (this.notifying) return; // queued: delivered after the current round
    this.notifying = true;
    try {
      while (this.notifyQueue.length > 0) {
        const current = this.notifyQueue.shift()!;
        this.pendingNotify.delete(current);
        const record = this.commits.get(current);
        if (!record || record.evicted) continue; // unreachable: pending versions are gc-protected
        const state = record.root;
        const round = Array.from(this.listeners);
        for (const listener of round) {
          if (!this.listeners.has(listener)) continue; // unsubscribed mid-round
          try {
            listener(state, current);
          } catch (error) {
            this.listenerErrors.push({ version: current, error });
            if (this.onListenerError) this.onListenerError(error, current);
          }
        }
      }
    } finally {
      this.notifying = false;
    }
  }

  /** Evict oldest reclaimable history while over capacity. */
  private gc(): void {
    if (this.arena.retained <= this.capacity) return;
    const records = Array.from(this.commits.values()).sort((a, b) => a.version - b.version);
    for (const record of records) {
      if (this.arena.retained <= this.capacity) break;
      if (record.evicted) continue;
      if (record.version === this.headVersion) continue;
      if (this.pins.has(record.version)) continue;
      if (this.pendingNotify.has(record.version)) continue;
      record.evicted = true;
      this.evictedCount += 1;
      this.arena.release(record.root);
    }
  }
}
