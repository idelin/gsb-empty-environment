import { parsePath, formatPath, isPrefixPath, type Path } from './path.ts';
import { parsePattern, type Pattern } from './pattern.ts';
import { Automaton } from './automaton.ts';
import { verifyNotifications } from './reference.ts';

// ---------------------------------------------------------------------------
// Operations & changes
// ---------------------------------------------------------------------------

export interface SetOp { id: number; kind: 'set'; path: Path; value: unknown }
export interface DeleteOp { id: number; kind: 'delete'; path: Path }
export interface MoveOp { id: number; kind: 'move'; from: Path; to: Path }
export type Op = SetOp | DeleteOp | MoveOp;

/** JSON-serializable op log entry; `Store.replay` rebuilds state from these. */
export type SerializableOp =
  | { kind: 'set'; path: string; value: unknown }
  | { kind: 'delete'; path: string }
  | { kind: 'move'; from: string; to: string };

/**
 * A single affected path produced by applying an op. Moves and array-index
 * shifts carry both the old and the new path; matching runs against both.
 */
export interface Change {
  opId: number;
  kind: 'set' | 'delete' | 'move' | 'shift';
  path: Path;      // affected (new) path
  oldPath?: Path;  // previous path, for 'move' and 'shift'
}

export interface Notification {
  subscriptionId: number;
  pattern: string;
  /** Ops that touched this subscription: de-duplicated by op id, in commit order. */
  ops: Op[];
  /** Matched changes, in commit order (a change may match via path or oldPath). */
  changes: Change[];
}

export type NotifyFn = (n: Notification) => void;

export interface Subscription {
  id: number;
  unsubscribe(): void;
}

// ---------------------------------------------------------------------------
// Tree helpers (copy-on-write: committed snapshots are never mutated)
// ---------------------------------------------------------------------------

function getAt(root: unknown, path: readonly string[]): unknown {
  let node = root as Record<string, unknown> | null | undefined;
  for (const seg of path) {
    if (node === null || node === undefined) return undefined;
    node = (node as Record<string, unknown>)[seg] as Record<string, unknown>;
  }
  return node;
}

function cloneContainer(v: unknown): Record<string, unknown> {
  if (Array.isArray(v)) return v.slice() as unknown as Record<string, unknown>;
  if (v !== null && typeof v === 'object') return { ...(v as Record<string, unknown>) };
  return {};
}

function cwSet(root: unknown, path: readonly string[], value: unknown): unknown {
  if (path.length === 0) return value;
  const clone = cloneContainer(root);
  const head = path[0];
  clone[head] = cwSet((root as Record<string, unknown> | null)?.[head], path.slice(1), value);
  return clone;
}

function cwDelete(root: unknown, path: readonly string[]): unknown {
  const clone = cloneContainer(root);
  const head = path[0];
  if (path.length === 1) {
    if (Array.isArray(clone)) (clone as unknown as unknown[]).splice(Number(head), 1);
    else delete clone[head];
  } else {
    clone[head] = cwDelete((root as Record<string, unknown>)[head], path.slice(1));
  }
  return clone;
}

/** All paths present in `value`, including `base` itself (deepest last per subtree). */
function collectPaths(value: unknown, base: Path, out: Path[] = []): Path[] {
  out.push(base);
  if (value !== null && typeof value === 'object') {
    for (const key of Object.keys(value as Record<string, unknown>)) {
      collectPaths((value as Record<string, unknown>)[key], [...base, key], out);
    }
  }
  return out;
}

function serializeOp(op: Op): SerializableOp {
  switch (op.kind) {
    case 'set': return { kind: 'set', path: formatPath(op.path), value: op.value };
    case 'delete': return { kind: 'delete', path: formatPath(op.path) };
    case 'move': return { kind: 'move', from: formatPath(op.from), to: formatPath(op.to) };
  }
}

// ---------------------------------------------------------------------------
// Transaction
// ---------------------------------------------------------------------------

/**
 * Ops are applied eagerly to a private copy-on-write working copy, so a
 * transaction sees its own writes (`tx.get`) while external readers keep
 * observing the last committed snapshot. Nothing escapes until commit swaps
 * the root atomically.
 */
export class Tx {
  readonly ops: Op[] = [];
  readonly changes: Change[] = [];
  private working: unknown;
  private allocOpId: () => number;

  constructor(committed: unknown, allocOpId: () => number) {
    this.working = committed;
    this.allocOpId = allocOpId;
  }

  get(path: string | readonly string[]): unknown {
    return getAt(this.working, parsePath(path));
  }

  set(path: string | readonly string[], value: unknown): void {
    const p = parsePath(path);
    const v = structuredClone(value);
    const op: SetOp = { id: this.allocOpId(), kind: 'set', path: p, value: v };
    this.working = cwSet(this.working, p, v);
    this.ops.push(op);
    this.changes.push({ opId: op.id, kind: 'set', path: p });
  }

  delete(path: string | readonly string[]): void {
    const p = parsePath(path);
    if (p.length === 0) throw new Error('cannot delete the root');
    const old = getAt(this.working, p);
    if (old === undefined) return; // deleting a missing path is a no-op
    const op: DeleteOp = { id: this.allocOpId(), kind: 'delete', path: p };
    const parentPath = p.slice(0, -1);
    const parent = getAt(this.working, parentPath);
    if (Array.isArray(parent)) {
      // Array element removal shifts every later sibling down by one. The
      // notification scope is explicit: indices i .. n-1 of the old array.
      const i = Number(p[p.length - 1]);
      const n = parent.length;
      for (const sub of collectPaths(old, p)) {
        this.changes.push({ opId: op.id, kind: 'delete', path: sub });
      }
      for (let j = i; j < n - 1; j++) {
        this.changes.push({
          opId: op.id,
          kind: 'shift',
          oldPath: [...parentPath, String(j + 1)],
          path: [...parentPath, String(j)],
        });
      }
      this.changes.push({ opId: op.id, kind: 'delete', path: [...parentPath, String(n - 1)] });
    } else {
      for (const sub of collectPaths(old, p)) {
        this.changes.push({ opId: op.id, kind: 'delete', path: sub });
      }
    }
    this.working = cwDelete(this.working, p);
    this.ops.push(op);
  }

  move(from: string | readonly string[], to: string | readonly string[]): void {
    const f = parsePath(from);
    const t = parsePath(to);
    if (f.length === 0) throw new Error('cannot move the root');
    const value = getAt(this.working, f);
    if (value === undefined) throw new Error(`move: nothing at ${formatPath(f)}`);
    if (isPrefixPath(f, t)) {
      if (t.length === f.length) return; // moving onto itself is a no-op
      throw new Error(`move: cannot move ${formatPath(f)} into its own subtree`);
    }
    const op: MoveOp = { id: this.allocOpId(), kind: 'move', from: f, to: t };
    // Old/new path diff for the whole moved subtree.
    for (const sub of collectPaths(value, f)) {
      const rel = sub.slice(f.length);
      this.changes.push({ opId: op.id, kind: 'move', oldPath: sub, path: [...t, ...rel] });
    }
    // Removing an array element also shifts its later siblings.
    const parentPath = f.slice(0, -1);
    const parent = getAt(this.working, parentPath);
    if (Array.isArray(parent)) {
      const i = Number(f[f.length - 1]);
      const n = parent.length;
      for (let j = i; j < n - 1; j++) {
        this.changes.push({
          opId: op.id,
          kind: 'shift',
          oldPath: [...parentPath, String(j + 1)],
          path: [...parentPath, String(j)],
        });
      }
      this.changes.push({ opId: op.id, kind: 'delete', path: [...parentPath, String(n - 1)] });
    }
    this.working = cwDelete(this.working, f);
    this.working = cwSet(this.working, t, value);
    this.ops.push(op);
  }
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

interface SubEntry {
  pattern: Pattern;
  patternStr: string;
  fn: NotifyFn;
}

export class Store {
  private committed: unknown;
  private subs = new Map<number, SubEntry>();
  private automaton = new Automaton();
  private nextSubId = 1;
  private nextOpId = 1;
  private queue: Tx[] = [];
  private staged: Array<() => void> = [];
  private committing = false;
  private inTx = false;
  private lastCommit: {
    subsSnapshot: Map<number, Pattern>;
    changes: Change[];
    notifications: Notification[];
  } | null = null;

  /** Every committed op, in order. Feeds `Store.replay`. */
  readonly opLog: SerializableOp[] = [];
  /** Automaton traversal metrics from notification matching. */
  readonly stats = { totalMatchVisited: 0, lastMatchVisited: 0 };

  constructor(initial: unknown = {}) {
    this.committed = structuredClone(initial);
  }

  get automatonStates(): number {
    return this.automaton.stateCount;
  }

  /** The committed snapshot. Do not mutate the returned value. */
  snapshot(): unknown {
    return this.committed;
  }

  /** Reads only ever observe committed state — never a transaction's working copy. */
  get(path?: string | readonly string[]): unknown {
    if (path === undefined) return this.committed;
    return getAt(this.committed, parsePath(path));
  }

  /**
   * Subscribes to a path pattern. Called outside a transaction it takes effect
   * immediately; called inside a tx callback or a notification callback it is
   * staged and takes effect at the transaction boundary (after the current
   * transaction's notifications are delivered), so it never observes the
   * transaction during which it was registered.
   */
  subscribe(pattern: string | readonly string[], fn: NotifyFn): Subscription {
    const id = this.nextSubId++;
    const patternStr = typeof pattern === 'string' ? pattern : formatPath(pattern);
    const parsed = parsePattern(pattern);
    const apply = () => {
      this.subs.set(id, { pattern: parsed, patternStr, fn });
      this.rebuildAutomaton();
    };
    if (this.inTx || this.committing) this.staged.push(apply);
    else apply();
    return { id, unsubscribe: () => this.unsubscribe(id) };
  }

  private unsubscribe(id: number): void {
    const apply = () => {
      this.subs.delete(id);
      this.rebuildAutomaton();
    };
    if (this.inTx || this.committing) this.staged.push(apply);
    else apply();
  }

  private rebuildAutomaton(): void {
    const next = new Automaton();
    for (const [id, s] of this.subs) next.add(id, s.pattern);
    this.automaton = next;
  }

  tx(fn: (t: Tx) => void): void {
    const t = new Tx(this.committed, () => this.nextOpId++);
    this.inTx = true;
    try {
      fn(t);
    } finally {
      this.inTx = false;
    }
    if (t.ops.length > 0) this.queue.push(t);
    this.drain();
  }

  /**
   * Reentrant writes (a tx opened from inside a notification callback) are
   * queued and committed only after the current delivery round completes.
   */
  private drain(): void {
    if (this.committing) return;
    this.committing = true;
    try {
      for (;;) {
        const batch = this.queue.shift();
        if (batch) {
          this.commitBatch(batch);
          continue;
        }
        if (this.staged.length > 0) {
          this.flushStaged();
          continue;
        }
        break;
      }
    } finally {
      this.committing = false;
    }
  }

  private flushStaged(): void {
    const fns = this.staged;
    this.staged = [];
    for (const f of fns) f();
  }

  private commitBatch(t: Tx): void {
    this.committed = t.working; // atomic swap: no intermediate state is ever visible
    for (const op of t.ops) this.opLog.push(serializeOp(op));
    const notifications = this.computeNotifications(t);
    this.lastCommit = {
      subsSnapshot: new Map([...this.subs].map(([id, s]) => [id, s.pattern])),
      changes: t.changes,
      notifications,
    };
    for (const n of notifications) this.subs.get(n.subscriptionId)!.fn(n);
    // Subscription adds/removes requested during this transaction take effect
    // at its boundary: after this batch's notifications have been delivered.
    if (this.staged.length > 0) this.flushStaged();
  }

  private computeNotifications(t: Tx): Notification[] {
    const opById = new Map(t.ops.map(o => [o.id, o]));
    const perSub = new Map<number, { ops: Map<number, Op>; changes: Change[] }>();
    let visited = 0;
    for (const ch of t.changes) {
      const r = this.automaton.match(ch.path);
      visited += r.visited;
      const ids = new Set(r.subs);
      if (ch.oldPath) {
        const r2 = this.automaton.match(ch.oldPath);
        visited += r2.visited;
        for (const id of r2.subs) ids.add(id);
      }
      for (const id of ids) {
        let e = perSub.get(id);
        if (!e) {
          e = { ops: new Map(), changes: [] };
          perSub.set(id, e);
        }
        // De-duplicated by op id; Map preserves first-seen (commit) order.
        e.ops.set(ch.opId, opById.get(ch.opId)!);
        e.changes.push(ch);
      }
    }
    this.stats.lastMatchVisited = visited;
    this.stats.totalMatchVisited += visited;
    return [...perSub.entries()]
      .sort((a, b) => a[0] - b[0])
      .map(([subscriptionId, e]) => ({
        subscriptionId,
        pattern: this.subs.get(subscriptionId)!.patternStr,
        ops: [...e.ops.values()],
        changes: e.changes,
      }));
  }

  /**
   * Independent per-subscription re-verification of the last commit's
   * notifications (reference matcher instead of the shared automaton).
   * Returns a list of mismatches; empty means both implementations agree.
   */
  verifyLastCommit(): string[] {
    if (!this.lastCommit) return [];
    return verifyNotifications(
      this.lastCommit.subsSnapshot,
      this.lastCommit.changes,
      this.lastCommit.notifications,
    );
  }

  /** Rebuilds a store by replaying a committed op log. */
  static replay(log: readonly SerializableOp[], initial: unknown = {}): Store {
    const store = new Store(initial);
    for (const op of log) {
      store.tx(t => {
        if (op.kind === 'set') t.set(op.path, op.value);
        else if (op.kind === 'delete') t.delete(op.path);
        else t.move(op.from, op.to);
      });
    }
    return store;
  }
}
