/**
 * Transactional batched state store.
 *
 * Semantics:
 * - All operations enqueued inside one `batch()` (the scheduling cycle) are
 *   evaluated IN ORDER against a private transaction draft, then committed
 *   exactly once. Readers (`getState`, subscribers) never observe the draft.
 * - Direct assignments and functional updates keep their relative order;
 *   functional updates read the value produced by the preceding operation
 *   (so two `x => x + 1` in a row add 2, not 1).
 * - Nested batches join the outermost transaction; only the outermost batch
 *   commits and notifies subscribers (exactly one notification per cycle).
 * - If any operation throws (including a violated cross-field invariant),
 *   the whole batch is rolled back: no partial values, no notification.
 * - Individual operations can be cancelled before commit; cancelling one
 *   operation never undoes later independent operations.
 * - Operations may carry an `opId`; an already-applied `opId` is skipped,
 *   which makes retries idempotent.
 * - Every applied operation is appended to an in-memory log as a resolved
 *   key/value pair, so state can be replayed from the initial snapshot.
 */

export type Condition<S> = (draft: Readonly<S>) => boolean;
export type Updater<S, K extends keyof S> = (prev: S[K], draft: Readonly<S>) => S[K];
export type Listener<S> = (state: Readonly<S>, prev: Readonly<S>) => void;

export interface OpOptions {
  /** Idempotency key: an opId that was already applied is skipped on retry. */
  opId?: string;
}

export interface OpHandle {
  readonly id: number;
  readonly cancelled: boolean;
  cancel(): void;
}

/** One applied state mutation, resolved to a concrete post-value. */
export interface LogEntry {
  commit: number;
  key: string;
  value: unknown;
  opId?: string;
}

type OpDef<S> =
  | { kind: 'set'; key: string; value: unknown }
  | { kind: 'update'; key: string; fn: (prev: never, draft: Readonly<S>) => unknown }
  | { kind: 'setIf'; key: string; when: Condition<S>; value: unknown }
  | { kind: 'updateIf'; key: string; when: Condition<S>; fn: (prev: never, draft: Readonly<S>) => unknown }
  | { kind: 'assert'; check: (draft: Readonly<S>) => boolean; message: string };

interface QueuedOp<S> {
  seq: number;
  opId?: string;
  def: OpDef<S>;
  cancelled: boolean;
}

function applyOp<S extends Record<string, unknown>>(def: OpDef<S>, draft: S): void {
  switch (def.kind) {
    case 'set':
      draft[def.key as keyof S] = def.value as S[keyof S];
      break;
    case 'update':
      draft[def.key as keyof S] = def.fn(draft[def.key as keyof S] as never, draft) as S[keyof S];
      break;
    case 'setIf':
      if (def.when(draft)) draft[def.key as keyof S] = def.value as S[keyof S];
      break;
    case 'updateIf':
      if (def.when(draft)) {
        draft[def.key as keyof S] = def.fn(draft[def.key as keyof S] as never, draft) as S[keyof S];
      }
      break;
    case 'assert':
      if (!def.check(draft)) throw new Error(`Invariant violated: ${def.message}`);
      break;
  }
}

export class Transaction<S extends Record<string, unknown>> {
  private readonly store: Store<S>;

  /** @internal */
  constructor(store: Store<S>) {
    this.store = store;
  }

  /** Direct assignment, applied in queue order. */
  set<K extends keyof S & string>(key: K, value: S[K], opts: OpOptions = {}): OpHandle {
    return this.store.enqueue({ kind: 'set', key, value }, opts);
  }

  /** Functional update: receives the value left by the previous operation. */
  update<K extends keyof S & string>(key: K, fn: Updater<S, K>, opts: OpOptions = {}): OpHandle {
    return this.store.enqueue({ kind: 'update', key, fn: fn as never }, opts);
  }

  /** Conditional direct assignment; `when` is evaluated on the draft, in order. */
  setIf<K extends keyof S & string>(key: K, when: Condition<S>, value: S[K], opts: OpOptions = {}): OpHandle {
    return this.store.enqueue({ kind: 'setIf', key, when, value }, opts);
  }

  /** Conditional functional update; `when` is evaluated on the draft, in order. */
  updateIf<K extends keyof S & string>(key: K, when: Condition<S>, fn: Updater<S, K>, opts: OpOptions = {}): OpHandle {
    return this.store.enqueue({ kind: 'updateIf', key, when, fn: fn as never }, opts);
  }

  /** Cross-field invariant checked on the draft at this point in the order. */
  assert(check: (draft: Readonly<S>) => boolean, message = 'assertion failed'): OpHandle {
    return this.store.enqueue({ kind: 'assert', check, message }, {});
  }

  /** Committed state only — readers never see the in-flight transaction draft. */
  read(): Readonly<S> {
    return this.store.getState();
  }
}

export class Store<S extends Record<string, unknown>> {
  private committed: S;
  private queue: Array<QueuedOp<S>> = [];
  private batchDepth = 0;
  private seq = 0;
  private commitCount = 0;
  private readonly listeners = new Set<Listener<S>>();
  private readonly appliedOpIds = new Set<string>();
  private readonly opLog: LogEntry[] = [];

  constructor(initial: S) {
    this.committed = { ...initial };
  }

  /** Committed state. Never exposes a transaction draft. */
  getState(): Readonly<S> {
    return this.committed;
  }

  /** Applied-operation log (resolved values), suitable for replay. */
  get log(): readonly LogEntry[] {
    return this.opLog;
  }

  get commits(): number {
    return this.commitCount;
  }

  hasApplied(opId: string): boolean {
    return this.appliedOpIds.has(opId);
  }

  subscribe(listener: Listener<S>): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  /** @internal */
  enqueue(def: OpDef<S>, opts: OpOptions): OpHandle {
    if (this.batchDepth === 0) {
      throw new Error('Operations must be enqueued inside store.batch(...)');
    }
    const op: QueuedOp<S> = { seq: ++this.seq, opId: opts.opId, def, cancelled: false };
    this.queue.push(op);
    return {
      id: op.seq,
      get cancelled() {
        return op.cancelled;
      },
      cancel() {
        op.cancelled = true;
      },
    };
  }

  /**
   * Runs `fn` inside a transaction. Nested calls join the outer transaction;
   * only the outermost call evaluates the queue and commits (one notification).
   */
  batch(fn: (tx: Transaction<S>) => void): void {
    const outermost = this.batchDepth === 0;
    this.batchDepth += 1;
    try {
      fn(new Transaction<S>(this));
    } catch (err) {
      if (outermost) this.queue.length = 0; // discard the whole transaction
      throw err;
    } finally {
      this.batchDepth -= 1;
    }
    if (outermost) this.commit();
  }

  private commit(): void {
    const ops = this.queue;
    this.queue = [];
    if (ops.length === 0) return;

    const draft = { ...this.committed };
    const entries: LogEntry[] = [];
    const commitId = this.commitCount + 1;
    const newOpIds: string[] = [];

    try {
      for (const op of ops) {
        if (op.cancelled) continue;
        if (op.opId !== undefined && this.appliedOpIds.has(op.opId)) continue; // idempotent retry
        applyOp(op.def, draft);
        if (op.opId !== undefined) {
          this.appliedOpIds.add(op.opId);
          newOpIds.push(op.opId);
        }
        if (op.def.kind !== 'assert') {
          const entry: LogEntry = { commit: commitId, key: op.def.key, value: draft[op.def.key as keyof S] };
          if (op.opId !== undefined) entry.opId = op.opId;
          entries.push(entry);
        }
      }
    } catch (err) {
      // Roll back: the draft is discarded and idempotency marks are undone,
      // so a failed batch leaves no partial values and can be retried safely.
      for (const id of newOpIds) this.appliedOpIds.delete(id);
      throw err;
    }

    if (entries.length === 0) return; // nothing applied (all cancelled/deduped)

    const prev = this.committed;
    this.committed = draft;
    this.commitCount = commitId;
    this.opLog.push(...entries);
    for (const listener of [...this.listeners]) listener(this.committed, prev);
  }

  /** Replays a resolved operation log on top of `initial`. */
  static replay<S extends Record<string, unknown>>(initial: S, log: readonly LogEntry[]): S {
    const state = { ...initial };
    for (const entry of log) state[entry.key as keyof S] = entry.value as S[keyof S];
    return state;
  }
}
