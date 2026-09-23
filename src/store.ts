/**
 * Projection store.
 *
 * Guarantees implemented here:
 * - Subscribers observe selector projections only; a configurable equality
 *   function suppresses no-change notifications (per selector and per
 *   subscription).
 * - Transactions apply many mutations but publish a single final diff;
 *   readers (`getState` / `get`) never observe intermediate transaction state.
 * - Writes re-entered from listeners are committed as new transactions and
 *   their notifications are queued behind the current dispatch.
 * - An equality function that throws never breaks other subscribers: the
 *   error is counted, the `onEqualityError` policy decides (default: treat as
 *   changed and notify), and dispatch continues.
 * - With `verify: true`, every commit independently recomputes each live
 *   selector's full projection from scratch (bypassing all memo caches) and
 *   compares it against the cached value.
 * - Every state operation is recorded in an action log and can be replayed.
 */

import {
  createReadTracker,
  deepFreeze,
  diffPaths,
  getAtPath,
  pathsIntersect,
  setAtPathImmutable,
  type Path,
} from './paths.ts';
import {
  nodeOf,
  type AnySelector,
  type Equality,
  type Selector,
  type SelectorNodeDef,
} from './selector.ts';

export type EqualityErrorPolicy = 'notify' | 'suppress';

export interface StoreOptions {
  /** Deep-freeze state and every selector result (protects mutable projections). */
  freeze?: boolean;
  /** Independently recompute full projections after each commit and compare. */
  verify?: boolean;
  /**
   * Called when an equality function throws. Return 'suppress' to skip that
   * notification; any other result (default) treats the value as changed.
   */
  onEqualityError?: (error: unknown, context: string) => EqualityErrorPolicy | void;
  /** Called with a description when verification finds a mismatch. */
  onVerificationFailed?: (detail: string) => void;
  /** Safety bound on commits triggered from within notification dispatch. */
  maxFlushCommits?: number;
}

export interface SubscribeOptions<T> {
  /** Per-subscription equality, overriding the selector's equality. */
  equality?: Equality<T>;
  /** Deliver a structured clone to the listener instead of the shared value. */
  snapshot?: boolean;
}

export type Listener<T> = (value: T, previous: T | undefined) => void;
export type Unsubscribe = () => void;

export type Action =
  | { readonly type: 'set'; readonly path: readonly (string | number)[]; readonly value: unknown }
  | { readonly type: 'replace'; readonly value: unknown };

export interface StoreStats {
  commits: number;
  notifications: number;
  recomputes: number;
  suppressed: number;
  equalityErrors: number;
  verificationFailures: number;
}

export interface NodeStats {
  recomputes: number;
  version: number;
}

interface NodeState {
  value: unknown;
  version: number;
  evaluated: boolean;
  inputVersions: number[];
  readPaths: Path[];
  recomputes: number;
}

interface Subscription {
  readonly def: SelectorNodeDef;
  readonly equality: Equality<unknown>;
  readonly listener: Listener<unknown>;
  readonly snapshot: boolean;
  active: boolean;
  lastValue: unknown;
  lastVersion: number;
  hasValue: boolean;
}

export interface Transaction {
  set: (path: Path, value: unknown) => void;
  update: <T>(path: Path, fn: (current: T) => T) => void;
  replace: (nextRoot: unknown) => void;
}

export interface Store<S> {
  getState: () => S;
  get: <T>(selector: Selector<T>) => T;
  subscribe: <T>(
    selector: Selector<T>,
    listener: Listener<T>,
    options?: SubscribeOptions<T>,
  ) => Unsubscribe;
  transaction: (fn: (tx: Transaction) => void) => void;
  set: (path: Path, value: unknown) => void;
  update: <T>(path: Path, fn: (current: T) => T) => void;
  replace: (nextRoot: S) => void;
  readonly actionLog: readonly Action[];
  replay: (actions?: readonly Action[]) => void;
  readonly stats: StoreStats;
  nodeStats: (selector: AnySelector) => NodeStats;
  readonly verificationLog: readonly string[];
}

/** Purely applies an action log to a state; used by replay. */
export function applyActions<S>(initial: S, actions: readonly Action[]): S {
  let state: unknown = initial;
  for (const action of actions) {
    state =
      action.type === 'replace'
        ? action.value
        : setAtPathImmutable(state, action.path, action.value);
  }
  return state as S;
}

export function createStore<S>(initialState: S, options: StoreOptions = {}): Store<S> {
  const freeze = options.freeze ?? false;
  const verify = options.verify ?? false;
  const maxFlushCommits = options.maxFlushCommits ?? 100;

  const baseState = freeze ? deepFreeze(initialState) : initialState;
  let committed: S = baseState;
  const actionLog: Action[] = [];
  const verificationLog: string[] = [];

  const nodeStates = new Map<SelectorNodeDef, NodeState>();
  let nodeOrder: SelectorNodeDef[] = [];
  const subscriptions = new Set<Subscription>();

  const stats: StoreStats = {
    commits: 0,
    notifications: 0,
    recomputes: 0,
    suppressed: 0,
    equalityErrors: 0,
    verificationFailures: 0,
  };

  let pendingSubs: Subscription[] = [];
  const pendingSet = new Set<Subscription>();
  let flushing = false;
  let flushCommits = 0;
  let activeTx: { working: unknown; ops: Action[] } | null = null;

  function safeEquality(
    equality: Equality<unknown>,
    a: unknown,
    b: unknown,
    context: string,
  ): boolean {
    try {
      return equality(a, b);
    } catch (error) {
      stats.equalityErrors += 1;
      const policy = options.onEqualityError?.(error, context);
      return policy === 'suppress';
    }
  }

  function registerNode(def: SelectorNodeDef): NodeState {
    const existing = nodeStates.get(def);
    if (existing) return existing;
    for (const input of def.inputs) registerNode(nodeOf(input));
    const state: NodeState = {
      value: undefined,
      version: 0,
      evaluated: false,
      inputVersions: [],
      readPaths: [],
      recomputes: 0,
    };
    nodeStates.set(def, state);
    nodeOrder.push(def);
    nodeOrder.sort((a, b) => a.depth - b.depth);
    return state;
  }

  function evaluateNode(def: SelectorNodeDef, state: NodeState): void {
    let value: unknown;
    if (def.kind === 'reader' && def.reader) {
      const tracker = createReadTracker(committed);
      value = tracker.unwrap(def.reader(tracker.proxy as never));
      state.readPaths = tracker.paths;
    } else if (def.combiner) {
      for (const input of def.inputs) {
        const inputDef = nodeOf(input);
        const inputState = registerNode(inputDef);
        if (!inputState.evaluated) evaluateNode(inputDef, inputState);
      }
      const args = def.inputs.map((input) => nodeStates.get(nodeOf(input))?.value);
      value = def.combiner(...(args as never[]));
    }
    state.recomputes += 1;
    stats.recomputes += 1;
    if (freeze) deepFreeze(value);
    const changed =
      !state.evaluated ||
      !safeEquality(def.equality, state.value, value, `selector#${def.id}`);
    if (changed) {
      state.value = value;
      state.version += 1;
    } else if (state.evaluated) {
      stats.suppressed += 1; // recomputed but equality judged it unchanged
    }
    state.evaluated = true;
    state.inputVersions = def.inputs.map(
      (input) => nodeStates.get(nodeOf(input))?.version ?? 0,
    );
  }

  function settle(changedPaths: Path[]): void {
    for (const def of nodeOrder) {
      const state = nodeStates.get(def);
      if (!state) continue;
      if (!state.evaluated) {
        evaluateNode(def, state);
        continue;
      }
      if (def.kind === 'reader') {
        const affected = state.readPaths.some((read) =>
          changedPaths.some((changed) => pathsIntersect(read, changed)),
        );
        if (affected) evaluateNode(def, state);
      } else {
        const stale = def.inputs.some((input, index) => {
          const inputState = nodeStates.get(nodeOf(input));
          return (inputState?.version ?? 0) !== state.inputVersions[index];
        });
        if (stale) evaluateNode(def, state);
      }
    }
  }

  /** Independent full-projection reference check: no memo caches involved. */
  function freshEvaluate(def: SelectorNodeDef): unknown {
    if (def.kind === 'reader' && def.reader) {
      return def.reader(committed as never);
    }
    if (def.combiner) {
      return def.combiner(...(def.inputs.map((input) => freshEvaluate(nodeOf(input))) as never[]));
    }
    return undefined;
  }

  function runVerification(): void {
    for (const def of nodeOrder) {
      const state = nodeStates.get(def);
      if (!state?.evaluated) continue;
      const fresh = freshEvaluate(def);
      let equal = false;
      try {
        equal = def.equality(state.value, fresh);
      } catch {
        equal = false;
      }
      if (!equal) {
        stats.verificationFailures += 1;
        const detail = `selector#${def.id} cached projection diverges from independent recomputation`;
        verificationLog.push(detail);
        options.onVerificationFailed?.(detail);
      }
    }
  }

  function enqueueSubscription(sub: Subscription): void {
    if (!sub.active || pendingSet.has(sub)) return;
    pendingSet.add(sub);
    pendingSubs.push(sub);
  }

  function enqueueAll(): void {
    for (const sub of subscriptions) {
      const state = nodeStates.get(sub.def);
      if (!state?.evaluated) continue;
      if (sub.hasValue && state.version === sub.lastVersion) continue;
      enqueueSubscription(sub);
    }
  }

  function flush(): void {
    if (flushing) return;
    flushing = true;
    try {
      while (pendingSubs.length > 0) {
        const sub = pendingSubs.shift() as Subscription;
        pendingSet.delete(sub);
        if (!sub.active) continue; // unsubscribed while queued: drop delivery
        const state = nodeStates.get(sub.def);
        if (!state?.evaluated) continue;
        if (sub.hasValue && state.version === sub.lastVersion) continue; // coalesced
        if (
          sub.hasValue &&
          safeEquality(sub.equality, sub.lastValue, state.value, `subscription#${sub.def.id}`)
        ) {
          sub.lastVersion = state.version;
          stats.suppressed += 1;
          continue;
        }
        const previous = sub.lastValue;
        sub.lastValue = state.value;
        sub.lastVersion = state.version;
        sub.hasValue = true;
        const delivered = sub.snapshot ? structuredClone(state.value) : state.value;
        sub.listener(delivered, previous);
        stats.notifications += 1;
      }
    } finally {
      flushing = false;
      flushCommits = 0;
    }
  }

  function commit(nextState: unknown): void {
    const changedPaths: Path[] = [];
    diffPaths(committed, nextState, [], changedPaths);
    if (changedPaths.length === 0) return; // e.g. root replaced by an equal projection
    if (flushing) {
      flushCommits += 1;
      if (flushCommits > maxFlushCommits) {
        throw new Error(
          `reentrant commit limit (${maxFlushCommits}) exceeded during notification dispatch`,
        );
      }
    }
    committed = nextState as S;
    stats.commits += 1;
    settle(changedPaths);
    if (verify) runVerification();
    enqueueAll();
    flush();
  }

  const txApi: Transaction = {
    set(path, value) {
      if (!activeTx) throw new Error('transaction handle used outside of transaction()');
      activeTx.working = setAtPathImmutable(activeTx.working, path, value);
      activeTx.ops.push({ type: 'set', path: [...path], value });
    },
    update(path, fn) {
      if (!activeTx) throw new Error('transaction handle used outside of transaction()');
      const next = fn(getAtPath(activeTx.working, path) as never);
      activeTx.working = setAtPathImmutable(activeTx.working, path, next);
      activeTx.ops.push({ type: 'set', path: [...path], value: next });
    },
    replace(nextRoot) {
      if (!activeTx) throw new Error('transaction handle used outside of transaction()');
      activeTx.working = nextRoot;
      activeTx.ops.push({ type: 'replace', value: nextRoot });
    },
  };

  function transaction(fn: (tx: Transaction) => void): void {
    const isOuter = activeTx === null;
    if (isOuter) activeTx = { working: committed, ops: [] };
    const tx = activeTx as { working: unknown; ops: Action[] };
    try {
      fn(txApi);
    } finally {
      if (isOuter) activeTx = null;
    }
    if (isOuter) {
      actionLog.push(...tx.ops);
      commit(tx.working);
    }
  }

  const store: Store<S> = {
    getState: () => committed,

    get<T>(selector: Selector<T>): T {
      const def = nodeOf(selector);
      const state = registerNode(def);
      if (!state.evaluated) evaluateNode(def, state);
      return state.value as T;
    },

    subscribe<T>(
      selector: Selector<T>,
      listener: Listener<T>,
      subscribeOptions: SubscribeOptions<T> = {},
    ): Unsubscribe {
      const def = nodeOf(selector);
      const state = registerNode(def);
      if (!state.evaluated) evaluateNode(def, state);
      const sub: Subscription = {
        def,
        equality: (subscribeOptions.equality ?? def.equality) as Equality<unknown>,
        listener: listener as Listener<unknown>,
        snapshot: subscribeOptions.snapshot ?? false,
        active: true,
        lastValue: state.value,
        lastVersion: state.version,
        hasValue: true,
      };
      subscriptions.add(sub);
      return () => {
        sub.active = false;
        subscriptions.delete(sub);
      };
    },

    transaction,
    set: (path, value) => transaction((tx) => tx.set(path, value)),
    update: (path, fn) => transaction((tx) => tx.update(path, fn)),
    replace: (nextRoot) => transaction((tx) => tx.replace(nextRoot)),

    get actionLog() {
      return actionLog;
    },

    replay(actions = actionLog) {
      const next = applyActions(baseState, actions);
      actionLog.length = 0;
      actionLog.push(...actions);
      commit(next);
    },

    get stats() {
      return { ...stats };
    },

    nodeStats(selector) {
      const state = nodeStates.get(nodeOf(selector));
      return { recomputes: state?.recomputes ?? 0, version: state?.version ?? 0 };
    },

    get verificationLog() {
      return verificationLog;
    },
  };

  return store;
}
