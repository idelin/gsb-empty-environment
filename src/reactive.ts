/**
 * reactive.ts — a dependency-tracking reactive engine.
 *
 * Properties:
 *  - Computeds track the dependencies they *actually* read at run time.
 *    When a branch changes, stale dependencies are unsubscribed.
 *  - Writes inside `batch` are atomic: effects and external readers only
 *    observe the committed snapshot, never intermediate states.
 *  - Writes issued from inside a compute callback are deferred to the next
 *    transaction round, so a computation always runs on one consistent
 *    snapshot and never observes its own side effects.
 *  - Cycles are detected during refresh and reported as a concrete chain
 *    of node names. A cycle that later breaks recovers automatically.
 *  - Every applied state write is recorded in an operation log that can be
 *    replayed against a freshly built world to reproduce the same state.
 */

export class CycleError extends Error {
  readonly chain: string[];
  constructor(chain: string[]) {
    super(`dependency cycle detected: ${chain.join(' -> ')}`);
    this.name = 'CycleError';
    this.chain = chain;
  }
}

export class UnstableError extends Error {
  constructor(rounds: number) {
    super(`system did not stabilize after ${rounds} rounds (a compute callback keeps writing)`);
    this.name = 'UnstableError';
  }
}

export type Op =
  | { readonly type: 'set'; readonly name: string; readonly value: unknown }
  | { readonly type: 'commit' };

export interface Source<T> {
  get(): T;
  readonly name: string;
}

const CLEAN = 0;
const MAYBE_DIRTY = 1;
const DIRTY = 2;
const CHECKING = 3;
const COMPUTING = 4;

type StateName = 0 | 1 | 2 | 3 | 4;

type AnySource = StateNode<unknown> | ComputedNode<unknown>;
type Subscriber = ComputedNode<unknown> | EffectNode;

class StateNode<T> {
  readonly world: World;
  readonly name: string;
  value: T;
  version = 0;
  readonly subs = new Set<Subscriber>();
  constructor(world: World, name: string, initial: T) {
    this.world = world;
    this.name = name;
    this.value = initial;
  }
}

class ComputedNode<T> {
  readonly world: World;
  readonly name: string;
  readonly fn: () => T;
  deps = new Map<AnySource, number>();
  readonly subs = new Set<Subscriber>();
  state: StateName = DIRTY;
  value: T | undefined = undefined;
  error: unknown = undefined;
  hasError = false;
  version = 0;
  recomputeCount = 0;
  nextDeps = new Map<AnySource, number>();
  constructor(world: World, name: string, fn: () => T) {
    this.world = world;
    this.name = name;
    this.fn = fn;
  }
}

class EffectNode {
  readonly world: World;
  readonly name: string;
  readonly fn: () => void;
  deps = new Map<AnySource, number>();
  state: StateName = DIRTY;
  runCount = 0;
  nextDeps = new Map<AnySource, number>();
  constructor(world: World, name: string, fn: () => void) {
    this.world = world;
    this.name = name;
    this.fn = fn;
  }
}

function markDirty(node: AnySource): void {
  for (const sub of node.subs) {
    if (sub instanceof EffectNode) {
      if (sub.state === CLEAN) sub.state = MAYBE_DIRTY;
      sub.world.scheduleEffect(sub);
    } else if (sub.state === CLEAN) {
      sub.state = MAYBE_DIRTY;
      markDirty(sub);
    }
  }
}

export class State<T> implements Source<T> {
  readonly node: StateNode<T>;
  constructor(node: StateNode<T>) {
    this.node = node;
  }
  get name(): string {
    return this.node.name;
  }
  get(): T {
    return this.node.world.readState(this.node);
  }
  set(value: T): void {
    this.node.world.setState(this.node, value);
  }
  update(fn: (prev: T) => T): void {
    this.set(fn(this.get()));
  }
}

export class Computed<T> implements Source<T> {
  readonly node: ComputedNode<T>;
  constructor(node: ComputedNode<T>) {
    this.node = node;
  }
  get name(): string {
    return this.node.name;
  }
  get(): T {
    return this.node.world.readComputed(this.node);
  }
  get recomputeCount(): number {
    return this.node.recomputeCount;
  }
}

export class Effect {
  readonly node: EffectNode;
  constructor(node: EffectNode) {
    this.node = node;
  }
  get name(): string {
    return this.node.name;
  }
  get runCount(): number {
    return this.node.runCount;
  }
}

export class World {
  /** Writes deferred because they were issued inside a compute callback. */
  private deferredWrites: Array<() => void> = [];
  private scheduledEffects: EffectNode[] = [];
  private txDepth = 0;
  private computeDepth = 0;
  private flushing = false;
  private currentObserver: ComputedNode<unknown> | EffectNode | null = null;
  private readonly refreshStack: Array<ComputedNode<unknown>> = [];
  private readonly states = new Map<string, StateNode<unknown>>();
  private readonly computeds = new Map<string, ComputedNode<unknown>>();
  private readonly opLog: Op[] = [];
  private readonly maxRounds = 100;
  /** Test hook: called after every commit, before deferred rounds are applied. */
  afterCommit: (() => void) | null = null;

  state<T>(name: string, initial: T): State<T> {
    if (this.states.has(name) || this.computeds.has(name)) {
      throw new Error(`duplicate node name: ${name}`);
    }
    const node = new StateNode<T>(this, name, initial);
    this.states.set(name, node as StateNode<unknown>);
    return new State(node);
  }

  computed<T>(name: string, fn: () => T): Computed<T> {
    if (this.states.has(name) || this.computeds.has(name)) {
      throw new Error(`duplicate node name: ${name}`);
    }
    const node = new ComputedNode<T>(this, name, fn);
    this.computeds.set(name, node as ComputedNode<unknown>);
    return new Computed(node);
  }

  effect(name: string, fn: () => void): Effect {
    const node = new EffectNode(this, name, fn);
    this.scheduleEffect(node);
    this.flush();
    return new Effect(node);
  }

  batch<T>(fn: () => T): T {
    this.txDepth++;
    try {
      return fn();
    } finally {
      this.txDepth--;
      if (this.txDepth === 0) this.flush();
    }
  }

  /** All applied writes, grouped into transactions by `commit` markers. */
  get log(): readonly Op[] {
    return this.opLog;
  }

  /** Replay a recorded operation log against this (freshly built) world. */
  applyLog(ops: readonly Op[]): void {
    let open = false;
    for (const op of ops) {
      if (op.type === 'commit') {
        if (open) {
          this.txDepth--;
          open = false;
          this.flush();
        }
        continue;
      }
      if (!open) {
        this.txDepth++;
        open = true;
      }
      const node = this.states.get(op.name);
      if (!node) throw new Error(`replay: unknown state '${op.name}'`);
      this.applySet(node, op.value);
    }
    if (open) {
      this.txDepth--;
      this.flush();
    }
  }

  /** Names of all computeds, in construction order. */
  get computedNames(): string[] {
    return [...this.computeds.keys()];
  }

  recomputeCount(name: string): number {
    const node = this.computeds.get(name);
    if (!node) throw new Error(`unknown computed '${name}'`);
    return node.recomputeCount;
  }

  /** Current dependency edges of a computed (for inspection/tests). */
  depsOf(name: string): string[] {
    const node = this.computeds.get(name);
    if (!node) throw new Error(`unknown computed '${name}'`);
    return [...node.deps.keys()].map((dep) => dep.name);
  }

  readState<T>(node: StateNode<T>): T {
    this.recordDep(node as StateNode<unknown>);
    this.settleAfterRead();
    return node.value;
  }

  private settleAfterRead(): void {
    if (this.computeDepth === 0 && this.txDepth === 0) this.flush();
  }

  readComputed<T>(node: ComputedNode<T>): T {
    const n = node as ComputedNode<unknown>;
    try {
      this.refresh(n);
    } finally {
      // Record the dependency with the version observed *after* refresh, and
      // even when refresh fails, so the node is revisited once inputs change.
      this.recordDep(n);
    }
    if (n.hasError) throw n.error;
    this.settleAfterRead();
    return n.value as T;
  }

  setState<T>(node: StateNode<T>, value: T): void {
    if (Object.is(node.value, value)) return;
    const n = node as StateNode<unknown>;
    if (this.computeDepth > 0 || this.flushing) {
      // Isolation: a write issued while a compute callback (or the flush
      // phase) is running is deferred to the next transaction round.
      this.deferredWrites.push(() => this.applySet(n, value));
      return;
    }
    this.batch(() => this.applySet(n, value));
  }

  private applySet(node: StateNode<unknown>, value: unknown): void {
    node.value = value;
    node.version++;
    this.opLog.push({ type: 'set', name: node.name, value });
    markDirty(node);
  }

  private recordDep(node: AnySource): void {
    const observer = this.currentObserver;
    if (observer) observer.nextDeps.set(node, node.version);
  }

  private refresh(node: ComputedNode<unknown>): void {
    if (node.state === CLEAN) return;
    if (node.state === CHECKING || node.state === COMPUTING) {
      const idx = this.refreshStack.indexOf(node);
      const chain = this.refreshStack.slice(idx).map((n) => n.name);
      chain.push(node.name);
      throw new CycleError(chain);
    }
    const wasDirty = node.state === DIRTY;
    node.state = CHECKING;
    this.refreshStack.push(node);
    try {
      if (!wasDirty) {
        let stale = false;
        for (const [dep, seenVersion] of node.deps) {
          if (dep instanceof ComputedNode) this.refresh(dep);
          if (dep.version !== seenVersion) {
            stale = true;
            break;
          }
        }
        if (!stale) {
          node.state = CLEAN;
          return;
        }
      }
      this.recompute(node);
    } catch (error) {
      // A cycle reached through this node: store it as the node's result so
      // readers get a stable error and the node recovers once a dep changes.
      this.storeError(node, error);
      node.state = CLEAN;
    } finally {
      this.refreshStack.pop();
    }
  }

  private recompute(node: ComputedNode<unknown>): void {
    const prevObserver = this.currentObserver;
    this.currentObserver = node;
    node.state = COMPUTING;
    node.recomputeCount++;
    node.nextDeps = new Map();
    this.computeDepth++;
    try {
      const value = node.fn();
      this.storeValue(node, value);
    } catch (error) {
      this.storeError(node, error);
    } finally {
      this.computeDepth--;
      this.currentObserver = prevObserver;
      node.state = CLEAN;
      this.commitDeps(node);
    }
  }

  private storeValue(node: ComputedNode<unknown>, value: unknown): void {
    const changed = node.hasError || !Object.is(node.value, value);
    node.value = value;
    node.hasError = false;
    node.error = undefined;
    if (changed) node.version++;
  }

  private storeError(node: ComputedNode<unknown>, error: unknown): void {
    const changed = !node.hasError || node.error !== error;
    node.error = error;
    node.hasError = true;
    node.value = undefined;
    if (changed) node.version++;
  }

  private commitDeps(node: ComputedNode<unknown> | EffectNode): void {
    // Unsubscribe dependencies the last run no longer reads (branch switch),
    // subscribe newly read ones.
    for (const dep of node.deps.keys()) {
      if (!node.nextDeps.has(dep)) dep.subs.delete(node as Subscriber);
    }
    for (const dep of node.nextDeps.keys()) {
      if (!node.deps.has(dep)) dep.subs.add(node as Subscriber);
    }
    node.deps = node.nextDeps;
    node.nextDeps = new Map();
  }

  private scheduleEffect(node: EffectNode): void {
    this.scheduledEffects.push(node);
  }

  private runEffect(node: EffectNode): void {
    if (node.state === CLEAN) return;
    if (node.state !== DIRTY) {
      let stale = false;
      for (const [dep, seenVersion] of node.deps) {
        if (dep instanceof ComputedNode) this.refresh(dep);
        if (dep.version !== seenVersion) {
          stale = true;
          break;
        }
      }
      if (!stale) {
        node.state = CLEAN;
        return;
      }
    }
    const prevObserver = this.currentObserver;
    this.currentObserver = node;
    node.nextDeps = new Map();
    node.runCount++;
    try {
      node.fn();
    } finally {
      this.currentObserver = prevObserver;
      node.state = CLEAN;
      this.commitDeps(node);
    }
  }

  private flush(): void {
    if (this.flushing) return;
    this.flushing = true;
    try {
      let rounds = 0;
      for (;;) {
        while (this.scheduledEffects.length > 0) {
          const pending = this.scheduledEffects;
          this.scheduledEffects = [];
          for (const effect of pending) this.runEffect(effect);
        }
        const lastOp = this.opLog[this.opLog.length - 1];
        const committed = lastOp !== undefined && lastOp.type === 'set';
        if (committed) {
          this.opLog.push({ type: 'commit' });
          this.afterCommit?.();
        }
        if (this.deferredWrites.length === 0) break;
        // Apply writes deferred by compute callbacks / effects, one snapshot
        // per round, until the system reaches a fixed point.
        if (++rounds > this.maxRounds) {
          this.deferredWrites = [];
          throw new UnstableError(this.maxRounds);
        }
        const writes = this.deferredWrites;
        this.deferredWrites = [];
        this.txDepth++;
        try {
          for (const write of writes) write();
        } finally {
          this.txDepth--;
        }
      }
    } finally {
      this.flushing = false;
    }
  }
}
