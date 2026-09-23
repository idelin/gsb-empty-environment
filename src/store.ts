import { deepFreeze } from './freeze.ts';
import { structuralEqual } from './equal.ts';
import { SelectorGraph, defineSelector, type SelectorRef } from './graph.ts';
import { applyOp, getIn, replayOps, type Op, type Path } from './ops.ts';

export interface Stats {
  commits: number;
  recomputes: number;
  notifications: number;
  verifications: number;
  verificationFailures: number;
  equalityErrors: number;
  listenerErrors: number;
}

export interface StoreError {
  readonly error: unknown;
  readonly context: string;
}

export interface StoreOptions {
  freeze?: boolean;
  verifyProjections?: boolean;
  onError?: (error: unknown, context: string) => void;
}

export interface Tx {
  set(path: Path, value: unknown): void;
  unset(path: Path): void;
  merge(path: Path, value: Record<string, unknown>): void;
  replace(next: unknown): void;
  get(path: Path): unknown;
}

export interface SubscribeOptions<T> {
  equal?: (a: T, b: T) => boolean;
}

export interface Store<S> {
  readonly root: SelectorRef<S>;
  readonly stats: Stats;
  readonly errors: ReadonlyArray<StoreError>;
  readonly opLog: ReadonlyArray<ReadonlyArray<Op>>;
  readonly graph: SelectorGraph;
  getState(): S;
  getVersion(): number;
  transaction(fn: (tx: Tx) => void): void;
  update(path: Path, value: unknown): void;
  apply(ops: Op[]): void;
  replace(next: S): void;
  replay(ops: Op[]): void;
  select<T>(ref: SelectorRef<T>): T;
  subscribe<T>(
    ref: SelectorRef<T>,
    listener: (next: T, prev: T) => void,
    options?: SubscribeOptions<T>,
  ): () => void;
}

interface Subscription<T> {
  readonly ref: SelectorRef<T>;
  readonly listener: (next: T, prev: T) => void;
  readonly equal: (a: T, b: T) => boolean;
  active: boolean;
  value: T;
}

export function createStore<S>(initialState: S, options: StoreOptions = {}): Store<S> {
  const freeze = options.freeze ?? true;
  const verifyProjections = options.verifyProjections ?? true;
  const errors: StoreError[] = [];
  const reportError = (error: unknown, context: string): void => {
    errors.push({ error, context });
    options.onError?.(error, context);
  };
  const stats: Stats = {
    commits: 0,
    recomputes: 0,
    notifications: 0,
    verifications: 0,
    verificationFailures: 0,
    equalityErrors: 0,
    listenerErrors: 0,
  };
  const graph = new SelectorGraph({ freeze, onError: reportError, stats });

  let committed: S = freeze ? deepFreeze(initialState) : initialState;
  const initialSnapshot: S = committed;
  let draft: S = committed;
  let txDepth = 0;
  let pendingOps: Op[] = [];
  const opLog: Op[][] = [];

  const rootDef = defineSelector<S>({ id: 'root', compute: () => committed });
  const root: SelectorRef<S> = rootDef.with(undefined);

  const subscriptions = new Set<Subscription<unknown>>();
  let flushing = false;
  let flushRequested = false;

  const txApi: Tx = {
    set(path, value) {
      applyDraftOp({ type: 'set', path, value });
    },
    unset(path) {
      applyDraftOp({ type: 'unset', path });
    },
    merge(path, value) {
      applyDraftOp({ type: 'merge', path, value });
    },
    replace(next) {
      applyDraftOp({ type: 'set', path: [], value: next });
    },
    get(path) {
      return getIn(draft, path);
    },
  };

  function applyDraftOp(op: Op): void {
    pendingOps.push(op);
    draft = applyOp(draft, op) as S;
  }

  function commitIfNeeded(): void {
    if (txDepth > 0) return;
    if (pendingOps.length === 0 || draft === committed) {
      pendingOps = [];
      draft = committed;
      return;
    }
    committed = draft;
    stats.commits++;
    opLog.push(pendingOps);
    pendingOps = [];
    graph.advance();
    flush();
  }

  function transaction(fn: (tx: Tx) => void): void {
    txDepth++;
    let threw = true;
    try {
      fn(txApi);
      threw = false;
    } finally {
      txDepth--;
      if (txDepth === 0) {
        if (threw) {
          draft = committed;
          pendingOps = [];
        } else {
          commitIfNeeded();
        }
      }
    }
  }

  function flush(): void {
    if (flushing) {
      flushRequested = true;
      return;
    }
    flushing = true;
    try {
      do {
        flushRequested = false;
        for (const sub of [...subscriptions]) {
          if (!sub.active) continue;
          const next = graph.read(sub.ref);
          let changed: boolean;
          try {
            changed = !sub.equal(sub.value, next);
          } catch (error) {
            stats.equalityErrors++;
            reportError(error, `equal:subscription:${sub.ref.def.id}`);
            changed = true;
          }
          const prev = sub.value;
          sub.value = next;
          if (!changed || !sub.active) continue;
          stats.notifications++;
          try {
            sub.listener(next, prev);
          } catch (error) {
            stats.listenerErrors++;
            reportError(error, `listener:${sub.ref.def.id}`);
          }
        }
      } while (flushRequested);
      if (verifyProjections) verify();
    } finally {
      flushing = false;
    }
  }

  function verify(): void {
    for (const sub of subscriptions) {
      if (!sub.active) continue;
      stats.verifications++;
      let reference: unknown;
      try {
        reference = graph.evaluateReference(sub.ref);
      } catch (error) {
        stats.verificationFailures++;
        reportError(error, `verify:${sub.ref.def.id}`);
        continue;
      }
      if (!structuralEqual(sub.value, reference)) {
        stats.verificationFailures++;
        reportError(
          new Error(`projection verification mismatch for selector "${sub.ref.def.id}"`),
          'verify',
        );
      }
    }
  }

  const store: Store<S> = {
    root,
    stats,
    errors,
    opLog,
    graph,
    getState: () => committed,
    getVersion: () => graph.stateVersion,
    transaction,
    update(path, value) {
      transaction((tx) => tx.set(path, value));
    },
    apply(ops) {
      transaction((tx) => {
        for (const op of ops) applyDraftOp(op);
      });
    },
    replace(next) {
      transaction((tx) => tx.replace(next));
    },
    replay(ops) {
      transaction((tx) => tx.replace(replayOps(initialSnapshot, ops)));
    },
    select(ref) {
      return graph.read(ref);
    },
    subscribe(ref, listener, subscribeOptions = {}) {
      const sub: Subscription<unknown> = {
        ref: ref as SelectorRef<unknown>,
        listener: listener as (next: unknown, prev: unknown) => void,
        equal: (subscribeOptions.equal ?? ref.def.equal) as (a: unknown, b: unknown) => boolean,
        active: true,
        value: graph.read(ref),
      };
      subscriptions.add(sub);
      return () => {
        sub.active = false;
        subscriptions.delete(sub);
      };
    },
  };
  return store;
}
