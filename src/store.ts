import { Automaton } from './automaton.ts';
import { parsePattern } from './pattern.ts';
import type { Pattern } from './pattern.ts';
import { toPath, formatPath } from './path.ts';
import type { Path } from './path.ts';

export type Json = null | boolean | number | string | Json[] | { [key: string]: Json };

/** A journaled, replayable state operation. */
export type Op =
  | { id: number; kind: 'set'; path: Path; value: Json }
  | { id: number; kind: 'delete'; path: Path }
  | { id: number; kind: 'move'; from: Path; to: Path }
  | { id: number; kind: 'splice'; path: Path; index: number; deleteCount: number; items: Json[] };

/**
 * A notification unit derived from an op. `move` expands into a delete patch
 * on the old path plus a set patch on the new path (old/new path delta).
 * Array splices carry the inclusive index `range` affected by the shift.
 */
export interface Patch {
  opId: number;
  kind: 'set' | 'delete' | 'splice';
  path: Path;
  movedFrom?: Path;
  movedTo?: Path;
  range?: [number, number];
}

/** One deduplicated notification per subscription per commit. */
export interface Notification {
  subId: number;
  pattern: string;
  patches: Patch[];
  /** Operation identities, deduplicated, in operation order. */
  opIds: number[];
}

export interface CommitResult {
  ops: Op[];
  patches: Patch[];
  notifications: Notification[];
  /** Total automaton states visited while matching this commit's patches. */
  visited: number;
}

export type Callback = (notification: Notification, store: Store) => void;

type SubEntry = { source: string; pattern: Pattern; cb: Callback };
type SubChange =
  | { type: 'add'; id: number; entry: SubEntry }
  | { type: 'remove'; id: number };

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === 'object' && !Object.isFrozen(value)) {
    for (const key of Object.keys(value)) {
      deepFreeze((value as Record<string, unknown>)[key]);
    }
    Object.freeze(value);
  }
  return value;
}

function getAt(root: Json, path: Path): Json {
  let node: Json = root;
  for (const seg of path) {
    if (node === null || typeof node !== 'object') {
      throw new Error(`cannot descend into ${formatPath(path)} at segment '${seg}'`);
    }
    node = Array.isArray(node) ? node[Number(seg)] : (node as Record<string, Json>)[seg];
  }
  return node;
}

function parentOf(root: Json, path: Path): { parent: Json; key: string } {
  if (path.length === 0) throw new Error('cannot modify the root itself');
  const parent = getAt(root, path.slice(0, -1));
  if (parent === null || typeof parent !== 'object') {
    throw new Error(`parent of ${formatPath(path)} is not a container`);
  }
  return { parent, key: path[path.length - 1] };
}

function setAt(root: Json, path: Path, value: Json): void {
  // Auto-vivify missing intermediate objects.
  let node = root as Record<string, Json>;
  for (let i = 0; i < path.length - 1; i++) {
    const seg = path[i];
    let child: Json = Array.isArray(node) ? node[Number(seg)] : node[seg];
    if (child === null || typeof child !== 'object') {
      child = /^\d+$/.test(path[i + 1]) ? [] : {};
      if (Array.isArray(node)) node[Number(seg)] = child;
      else node[seg] = child;
    }
    node = child as Record<string, Json>;
  }
  const key = path[path.length - 1];
  if (Array.isArray(node)) node[Number(key)] = value;
  else node[key] = value;
}

function deleteAt(root: Json, path: Path): void {
  const { parent, key } = parentOf(root, path);
  if (Array.isArray(parent)) parent.splice(Number(key), 1);
  else delete (parent as Record<string, Json>)[key];
}

/** Paths the matcher must run for a patch. Splices expand their affected index range. */
export function matchPathsForPatch(patch: Patch): Path[] {
  if (patch.kind === 'splice' && patch.range) {
    const [lo, hi] = patch.range;
    const paths: Path[] = [];
    for (let i = lo; i <= hi; i++) paths.push([...patch.path, String(i)]);
    return paths;
  }
  return [patch.path];
}

export class Transaction {
  readonly ops: Op[] = [];
  readonly patches: Patch[] = [];
  private readonly draft: Json;
  private readonly store: Store;
  private closed = false;

  constructor(store: Store, base: Json) {
    this.store = store;
    this.draft = structuredClone(base);
  }

  get committedDraft(): Json {
    return this.draft;
  }

  private assertOpen(): void {
    if (this.closed) throw new Error('transaction already committed');
  }

  private allocOpId(): number {
    return this.store.allocOpId();
  }

  set(path: string | Path, value: Json): void {
    this.assertOpen();
    const p = toPath(path);
    setAt(this.draft, p, structuredClone(value));
    const id = this.allocOpId();
    this.ops.push({ id, kind: 'set', path: p, value: structuredClone(value) });
    this.patches.push({ opId: id, kind: 'set', path: p });
  }

  /** Object-key delete. On arrays it delegates to splice so indices stay dense. */
  delete(path: string | Path): void {
    this.assertOpen();
    const p = toPath(path);
    const { parent, key } = parentOf(this.draft, p);
    if (Array.isArray(parent)) {
      this.splice(p.slice(0, -1), Number(key), 1);
      return;
    }
    deleteAt(this.draft, p);
    const id = this.allocOpId();
    this.ops.push({ id, kind: 'delete', path: p });
    this.patches.push({ opId: id, kind: 'delete', path: p });
  }

  /** Node move: emits a delete patch on the old path and a set patch on the new path. */
  move(from: string | Path, to: string | Path): void {
    this.assertOpen();
    const fromPath = toPath(from);
    const toPath_ = toPath(to);
    const value = getAt(this.draft, fromPath);
    if (value === undefined) throw new Error(`nothing to move at ${formatPath(fromPath)}`);
    deleteAt(this.draft, fromPath);
    setAt(this.draft, toPath_, value);
    const id = this.allocOpId();
    this.ops.push({ id, kind: 'move', from: fromPath, to: toPath_ });
    this.patches.push(
      { opId: id, kind: 'delete', path: fromPath, movedTo: toPath_ },
      { opId: id, kind: 'set', path: toPath_, movedFrom: fromPath },
    );
  }

  /** Array splice; the notification range covers every index shifted or replaced. */
  splice(path: string | Path, index: number, deleteCount: number, ...items: Json[]): void {
    this.assertOpen();
    const p = toPath(path);
    const arr = getAt(this.draft, p);
    if (!Array.isArray(arr)) throw new Error(`not an array: ${formatPath(p)}`);
    const oldLen = arr.length;
    arr.splice(index, deleteCount, ...items.map((item) => structuredClone(item)));
    const newLen = arr.length;
    if (deleteCount === 0 && items.length === 0) return;
    const id = this.allocOpId();
    this.ops.push({ id, kind: 'splice', path: p, index, deleteCount, items: structuredClone(items) });
    const hi = Math.max(oldLen, newLen) - 1;
    const lo = Math.min(index, hi);
    this.patches.push({ opId: id, kind: 'splice', path: p, range: [lo, hi] });
  }

  close(): Json {
    this.closed = true;
    return this.draft;
  }
}

/**
 * Transactional store with path-based subscriptions.
 *
 * - Readers only ever see the committed root; a transaction mutates a private
 *   draft and the root is swapped atomically at commit, so intermediate
 *   transaction states are never observable.
 * - Every committed op is journaled and can be replayed into a fresh store.
 * - subscribe()/unsubscribe() called inside a transaction (or during
 *   notification delivery) are staged and take effect at the commit boundary;
 *   a commit's notifications go to the subscription set active when the
 *   transaction began.
 * - Reentrant transact() calls from notification callbacks are queued and run
 *   after the current delivery round, i.e. at the transaction boundary.
 */
export class Store {
  private committed: Json;
  private readonly journal: Op[] = [];
  private nextOpId = 1;
  private nextSubId = 1;
  private readonly subs = new Map<number, SubEntry>();
  private readonly automaton = new Automaton();
  private staged: SubChange[] = [];
  private openTx = 0;
  private delivering = false;
  private readonly queue: Array<() => void> = [];

  constructor(initial: Json = {}) {
    this.committed = deepFreeze(structuredClone(initial));
  }

  /** Committed, deeply frozen state. Safe to hand to readers. */
  get state(): Json {
    return this.committed;
  }

  /** Full operation journal (replayable). */
  get ops(): readonly Op[] {
    return this.journal;
  }

  /** Live states in the shared matching automaton. */
  get automatonStates(): number {
    return this.automaton.stateCount;
  }

  allocOpId(): number {
    return this.nextOpId++;
  }

  subscribe(patternSource: string, cb: Callback): number {
    const id = this.nextSubId++;
    const change: SubChange = {
      type: 'add',
      id,
      entry: { source: patternSource, pattern: parsePattern(patternSource), cb },
    };
    if (this.openTx > 0 || this.delivering) this.staged.push(change);
    else this.applySubChange(change);
    return id;
  }

  unsubscribe(id: number): void {
    const change: SubChange = { type: 'remove', id };
    if (this.openTx > 0 || this.delivering) this.staged.push(change);
    else this.applySubChange(change);
  }

  private applySubChange(change: SubChange): void {
    if (change.type === 'add') {
      this.subs.set(change.id, change.entry);
      this.automaton.add(change.id, change.entry.pattern);
    } else {
      const entry = this.subs.get(change.id);
      if (entry) {
        this.automaton.remove(change.id, entry.pattern);
        this.subs.delete(change.id);
      }
    }
  }

  /**
   * Run `fn` in a transaction and commit it. When invoked reentrantly from a
   * notification callback, the transaction is queued and committed after the
   * current delivery round; in that case `null` is returned.
   */
  transact(fn: (tx: Transaction) => void): CommitResult | null {
    if (this.delivering) {
      this.queue.push(() => {
        this.transact(fn);
      });
      return null;
    }
    this.openTx++;
    const tx = new Transaction(this, this.committed);
    let result: CommitResult;
    try {
      fn(tx);
      result = this.commit(tx);
    } finally {
      this.openTx--;
    }
    return result;
  }

  private commit(tx: Transaction): CommitResult {
    const ops = [...tx.ops];
    const patches = [...tx.patches];

    // 1. Match patches against the subscription set active at tx begin.
    const { notifications, visited } = this.dispatch(patches);

    // 2. Snapshot delivery callbacks before staged changes apply: a
    //    subscription removed at this boundary still receives this commit's
    //    notification (it was active when the transaction began).
    const delivery = notifications.map(
      (notification) => [notification, this.subs.get(notification.subId)?.cb] as const,
    );

    // 3. Atomically swap state and append to the journal.
    this.committed = deepFreeze(tx.close());
    this.journal.push(...ops);

    // 4. Staged subscription changes take effect at the commit boundary.
    const staged = this.staged;
    this.staged = [];
    for (const change of staged) this.applySubChange(change);

    // 5. Deliver after the state swap; reentrant writes queue up and run
    //    once this delivery round completes (transaction boundary).
    this.delivering = true;
    try {
      for (const [notification, cb] of delivery) {
        cb?.(notification, this);
      }
    } finally {
      this.delivering = false;
    }
    while (this.queue.length > 0) {
      this.queue.shift()!();
    }
    return { ops, patches, notifications, visited };
  }

  private dispatch(patches: Patch[]): { notifications: Notification[]; visited: number } {
    const bySub = new Map<number, Notification>();
    let visited = 0;
    for (const patch of patches) {
      const matched = new Set<number>();
      for (const path of matchPathsForPatch(patch)) {
        const result = this.automaton.match(path);
        visited += result.visited;
        for (const id of result.subs) matched.add(id);
      }
      for (const id of matched) {
        let notification = bySub.get(id);
        if (!notification) {
          notification = {
            subId: id,
            pattern: this.subs.get(id)?.source ?? '?',
            patches: [],
            opIds: [],
          };
          bySub.set(id, notification);
        }
        notification.patches.push(patch);
        if (!notification.opIds.includes(patch.opId)) notification.opIds.push(patch.opId);
      }
    }
    return { notifications: [...bySub.values()], visited };
  }

  /** Rebuild state by replaying a journal (e.g. `store.ops`) into this store. */
  replay(ops: readonly Op[]): void {
    this.transact((tx) => {
      for (const op of ops) {
        switch (op.kind) {
          case 'set':
            tx.set(op.path, op.value);
            break;
          case 'delete':
            tx.delete(op.path);
            break;
          case 'move':
            tx.move(op.from, op.to);
            break;
          case 'splice':
            tx.splice(op.path, op.index, op.deleteCount, ...op.items);
            break;
        }
      }
    });
  }
}
