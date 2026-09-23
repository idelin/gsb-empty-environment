import { isDeepStrictEqual } from 'node:util';

export type Path = readonly (string | number)[];

export interface StateOp {
  readonly kind: 'set';
  readonly path: Path;
  readonly value: unknown;
}

export interface CommitRecord {
  readonly version: number;
  readonly label: string;
  readonly ops: readonly StateOp[];
}

export interface NotifyMeta {
  readonly round: number;
  readonly fromVersion: number;
  readonly toVersion: number;
  readonly mergedCommits: readonly number[];
}

export type Selector<S, T> = (state: S) => T;
export type Equality<T> = (a: T, b: T) => boolean;
export type Listener<T> = (value: T, meta: NotifyMeta) => void;

export interface SubscriberError {
  readonly kind: 'listener' | 'selector' | 'cycle-budget';
  readonly subscriberId: number | null;
  readonly round: number;
  readonly version: number;
  readonly error: unknown;
}

export interface MergeEvidence {
  readonly round: number;
  readonly fromVersion: number;
  readonly toVersion: number;
  readonly mergedCommits: readonly number[];
}

export interface SubscriptionHandle<T> {
  readonly id: number;
  unsubscribe(): void;
  setSelector(selector: Selector<unknown, T>, equality?: Equality<T>): void;
  setEquality(equality: Equality<T>): void;
}

export const deepEqual: Equality<unknown> = (a, b) => isDeepStrictEqual(a, b);

export function applyOp<S>(state: S, op: StateOp): S {
  if (op.path.length === 0) return op.value as S;
  const [head, ...rest] = op.path;
  const base: Record<string | number, unknown> =
    state === null || state === undefined
      ? typeof head === 'number'
        ? []
        : {}
      : Array.isArray(state)
        ? ([...state] as unknown as Record<string | number, unknown>)
        : { ...(state as Record<string | number, unknown>) };
  base[head] = applyOp(base[head], { kind: 'set', path: rest, value: op.value });
  return base as S;
}

function getAt(state: unknown, path: Path): unknown {
  let cursor = state;
  for (const key of path) {
    cursor = (cursor as Record<string | number, unknown> | null | undefined)?.[key];
  }
  return cursor;
}

export class Transaction<S> {
  private draft: S;
  readonly ops: StateOp[] = [];

  constructor(base: S) {
    this.draft = base;
  }

  get(): S;
  get(path: Path): unknown;
  get(path?: Path): unknown {
    return path === undefined ? this.draft : getAt(this.draft, path);
  }

  set(path: Path, value: unknown): this {
    const op: StateOp = { kind: 'set', path, value };
    this.ops.push(op);
    this.draft = applyOp(this.draft, op);
    return this;
  }

  get result(): S {
    return this.draft;
  }
}

interface Subscription {
  id: number;
  selector: Selector<never, unknown>;
  equality: Equality<unknown>;
  listener: Listener<unknown>;
  lastValue: unknown;
  active: boolean;
}

interface PendingCommit {
  version: number;
  label: string;
  state: unknown;
}

export interface StoreOptions {
  roundBudget?: number;
}

export class Store<S> {
  private readonly initialState: S;
  private committed: S;
  private committedVersion = 0;
  private readonly commitLog: CommitRecord[] = [];
  private readonly subs = new Map<number, Subscription>();
  private nextSubId = 1;
  private pending: PendingCommit[] = [];
  private flushing = false;
  private inTransaction = false;
  private roundSeq = 0;
  private readonly roundBudget: number;
  private readonly errors: SubscriberError[] = [];
  private readonly merges: MergeEvidence[] = [];

  constructor(initialState: S, options: StoreOptions = {}) {
    this.initialState = initialState;
    this.committed = initialState;
    this.roundBudget = options.roundBudget ?? 64;
  }

  getState(): S {
    return this.committed;
  }

  getVersion(): number {
    return this.committedVersion;
  }

  getRound(): number {
    return this.roundSeq;
  }

  transact(label: string, fn: (tx: Transaction<S>) => void): { committed: boolean; version: number } {
    if (this.inTransaction) {
      throw new Error('nested transactions are not supported');
    }
    const tx = new Transaction<S>(this.committed);
    this.inTransaction = true;
    try {
      fn(tx);
    } finally {
      this.inTransaction = false;
    }
    if (tx.ops.length === 0) {
      return { committed: false, version: this.committedVersion };
    }
    this.committed = tx.result;
    this.committedVersion += 1;
    this.commitLog.push({ version: this.committedVersion, label, ops: [...tx.ops] });
    this.pending.push({ version: this.committedVersion, label, state: this.committed });
    this.flush();
    return { committed: true, version: this.committedVersion };
  }

  subscribe<T>(
    selector: Selector<S, T>,
    listener: Listener<T>,
    options: { equality?: Equality<T> } = {},
  ): SubscriptionHandle<T> {
    const id = this.nextSubId++;
    const sub: Subscription = {
      id,
      selector: selector as Selector<never, unknown>,
      equality: (options.equality ?? Object.is) as Equality<unknown>,
      listener: listener as Listener<unknown>,
      lastValue: selector(this.committed),
      active: true,
    };
    this.subs.set(id, sub);
    const store = this;
    return {
      id,
      unsubscribe(): void {
        sub.active = false;
        store.subs.delete(id);
      },
      setSelector(next: Selector<unknown, T>, equality?: Equality<T>): void {
        sub.selector = next as Selector<never, unknown>;
        if (equality !== undefined) sub.equality = equality as Equality<unknown>;
        sub.lastValue = (next as Selector<S, T>)(store.committed);
      },
      setEquality(equality: Equality<T>): void {
        sub.equality = equality as Equality<unknown>;
      },
    };
  }

  getSubscriberErrors(): readonly SubscriberError[] {
    return this.errors;
  }

  getMergeEvidence(): readonly MergeEvidence[] {
    return this.merges;
  }

  getCommitLog(): readonly CommitRecord[] {
    return this.commitLog;
  }

  replay(): { state: S; versions: number[] } {
    return Store.replayFrom(this.initialState, this.commitLog);
  }

  static replayFrom<S>(initialState: S, commits: readonly CommitRecord[]): { state: S; versions: number[] } {
    let state = initialState;
    const versions: number[] = [];
    for (const commit of commits) {
      for (const op of commit.ops) {
        state = applyOp(state, op);
      }
      versions.push(commit.version);
    }
    return { state, versions };
  }

  private flush(): void {
    if (this.flushing) return;
    this.flushing = true;
    let roundsInCascade = 0;
    try {
      while (this.pending.length > 0) {
        roundsInCascade += 1;
        if (roundsInCascade > this.roundBudget) {
          const dropped = this.pending.splice(0);
          this.errors.push({
            kind: 'cycle-budget',
            subscriberId: null,
            round: this.roundSeq + 1,
            version: dropped[dropped.length - 1].version,
            error: new Error(
              `cycle budget exceeded: dropped ${dropped.length} pending commit(s) after ${this.roundBudget} rounds`,
            ),
          });
          break;
        }
        this.roundSeq += 1;
        const commits = this.pending.splice(0);
        const toVersion = commits[commits.length - 1].version;
        const fromVersion = commits[0].version - 1;
        const mergedCommits = commits.map((c) => c.version);
        if (commits.length > 1) {
          this.merges.push({ round: this.roundSeq, fromVersion, toVersion, mergedCommits });
        }
        const state = commits[commits.length - 1].state as S;
        const meta: NotifyMeta = { round: this.roundSeq, fromVersion, toVersion, mergedCommits };
        const snapshot = [...this.subs.values()].filter((s) => s.active);
        for (const sub of snapshot) {
          let next: unknown;
          try {
            next = sub.selector(state);
          } catch (error) {
            this.errors.push({ kind: 'selector', subscriberId: sub.id, round: this.roundSeq, version: toVersion, error });
            continue;
          }
          if (sub.equality(sub.lastValue, next)) continue;
          sub.lastValue = next;
          try {
            sub.listener(next, meta);
          } catch (error) {
            this.errors.push({ kind: 'listener', subscriberId: sub.id, round: this.roundSeq, version: toVersion, error });
          }
        }
      }
    } finally {
      this.flushing = false;
    }
  }
}
