// Minimal reactive core.
//
// Semantics (see README.md):
//  - state() is a mutable cell; computed() is a derived node whose callback
//    re-runs only when a dependency it actually read last time changes.
//  - Dependencies are tracked dynamically while a callback executes.
//    After every run the new read set replaces the old one, so dependencies
//    disappear as soon as a branch stops reading them.
//  - Writes are batched in transactions. Computed nodes are never recomputed
//    inside a transaction: they expose the last committed snapshot. At commit
//    all writes apply on one consistent snapshot and subscribers (effects)
//    run exactly once, never seeing an intermediate state.
//  - Re-entrant writes requested from inside a computation are isolated to
//    the next commit round: the current round finishes on its consistent
//    snapshot first.
//  - Cycles are detected against the live evaluation stack and reported with
//    the concrete node-name chain.
//  - A failed computation adopts the partial dependency set it read before
//    throwing; the stored error is retried on the next invalidation.
//  - Every mutating public operation is appended to an append-only op log
//    that can be replayed into a fresh system.

export class CycleError extends Error {
  readonly chain: string[];
  constructor(chain: string[]) {
    super(`dependency cycle detected: ${chain.join(' -> ')}`);
    this.name = 'CycleError';
    this.chain = chain;
  }
}

export type Op =
  | { op: 'state'; name: string; value: unknown }
  | { op: 'computed'; name: string; fnId: string }
  | { op: 'effect'; name: string; fnId: string }
  | { op: 'set'; name: string; value: unknown }
  | { op: 'tx'; ops: Op[] };

export interface Cx {
  get(name: string): unknown;
  has(name: string): boolean;
  set(name: string, value: unknown): void;
}

export type Fn = (cx: Cx) => unknown;

export class FnRegistry {
  private readonly fns = new Map<string, Fn>();
  register(id: string, fn: Fn): void {
    if (this.fns.has(id)) throw new Error(`fnId already registered: ${id}`);
    this.fns.set(id, fn);
  }
  get(id: string): Fn {
    const fn = this.fns.get(id);
    if (!fn) throw new Error(`unknown fnId: ${id}`);
    return fn;
  }
}

export interface StateHandle<T> {
  readonly name: string;
  get(): T;
  set(value: T): void;
}

export interface ComputedHandle<T> {
  readonly name: string;
  get(): T;
}

export type NodeValue =
  | { status: 'ok'; value: unknown }
  | { status: 'error'; message: string; chain?: string[] };

interface StateNode {
  kind: 'state';
  id: number;
  name: string;
  value: unknown;
  subscribers: Set<ComputedNode | EffectNode>;
}

interface ComputedNode {
  kind: 'computed';
  id: number;
  name: string;
  fnId: string;
  deps: Set<AnyNode>;
  subscribers: Set<ComputedNode | EffectNode>;
  dirty: boolean;
  hasValue: boolean;
  value: unknown;
  hasError: boolean;
  error: unknown;
  runs: number;
}

interface EffectNode {
  kind: 'effect';
  id: number;
  name: string;
  fnId: string;
  deps: Set<AnyNode>;
  dirty: boolean;
  runs: number;
  hasOutput: boolean;
  lastOutput: unknown;
  lastError: unknown;
  hasError: boolean;
  notifications: unknown[];
}

type AnyNode = StateNode | ComputedNode | EffectNode;
type Observer = ComputedNode | EffectNode;

function cloneValue(value: unknown): unknown {
  if (
    value === null ||
    typeof value === 'number' ||
    typeof value === 'string' ||
    typeof value === 'boolean' ||
    typeof value === 'bigint' ||
    typeof value === 'undefined'
  ) {
    return value;
  }
  return structuredClone(value);
}

export function describeError(error: unknown): { message: string; chain?: string[] } {
  if (error instanceof CycleError) {
    return { message: error.message, chain: [...error.chain] };
  }
  if (error instanceof Error) return { message: error.message };
  return { message: String(error) };
}

export class ReactiveSystem {
  readonly log: Op[] = [];

  _replaying = false;

  private readonly nodes = new Map<number, AnyNode>();
  private readonly byName = new Map<string, AnyNode>();
  private nextId = 1;

  private observer: Observer | null = null;
  private collecting: Set<AnyNode> | null = null;
  private readonly computeStack: ComputedNode[] = [];

  private txDepth = 0;
  private flushing = false;
  private readDepth = 0;

  private staged = new Map<number, unknown>();
  private deferred = new Map<number, unknown>();
  private readonly frameBuffers: Map<number, unknown>[] = [];

  readonly commitErrors: { effect: string; error: unknown }[] = [];

  readonly registry: FnRegistry;

  constructor(registry: FnRegistry = new FnRegistry()) {
    this.registry = registry;
  }

  // ---- public construction API -----------------------------------------

  state<T>(name: string, initial: T): StateHandle<T> {
    const node: StateNode = {
      kind: 'state',
      id: this.nextId++,
      name,
      value: initial,
      subscribers: new Set(),
    };
    this.addNode(node);
    this.record({ op: 'state', name, value: cloneValue(initial) });
    const read = (): T => this.readNode(node) as T;
    const write = (value: T): void => {
      this.record({ op: 'set', name, value: cloneValue(value) });
      this.writeNode(node, value);
      this.commit();
    };
    return { name, get: read, set: write };
  }

  computed<T>(name: string, fnId: string, fn?: Fn): ComputedHandle<T> {
    if (fn) this.registry.register(fnId, fn);
    const node: ComputedNode = {
      kind: 'computed',
      id: this.nextId++,
      name,
      fnId,
      deps: new Set(),
      subscribers: new Set(),
      dirty: true,
      hasValue: false,
      value: undefined,
      hasError: false,
      error: undefined,
      runs: 0,
    };
    this.addNode(node);
    this.record({ op: 'computed', name, fnId });
    return { name, get: (): T => this.readComputed(node) as T };
  }

  effect(name: string, fnId: string, fn?: Fn): void {
    if (fn) this.registry.register(fnId, fn);
    const node: EffectNode = {
      kind: 'effect',
      id: this.nextId++,
      name,
      fnId,
      deps: new Set(),
      dirty: true,
      runs: 0,
      hasOutput: false,
      lastOutput: undefined,
      lastError: undefined,
      hasError: false,
      notifications: [],
    };
    this.addNode(node);
    this.record({ op: 'effect', name, fnId });
    this.commit();
  }

  tx(fn: () => void): void {
    if (this.flushing || this.frameBuffers.length > 0) {
      throw new Error('transactions cannot be opened during recomputation');
    }
    if (this.txDepth > 0) throw new Error('nested transactions are not supported');
    this.txDepth++;
    const base = this.log.length;
    try {
      fn();
    } catch (err) {
      this.staged.clear();
      if (!this._replaying) this.log.length = base;
      this.txDepth--;
      throw err;
    }
    this.txDepth--;
    if (!this._replaying) {
      const inner = this.log.splice(base);
      this.log.push({ op: 'tx', ops: inner });
    }
    this.commit();
  }

  // ---- introspection ----------------------------------------------------

  stats(): Record<string, number> {
    const out: Record<string, number> = {};
    for (const node of this.nodes.values()) {
      if (node.kind === 'computed' || node.kind === 'effect') out[node.name] = node.runs;
    }
    return out;
  }

  effectInfo(name: string): {
    runs: number;
    lastOutput: unknown;
    error: unknown;
    notifications: unknown[];
  } {
    const node = this.lookup(name);
    if (node.kind !== 'effect') throw new Error(`not an effect: ${name}`);
    return {
      runs: node.runs,
      lastOutput: node.lastOutput,
      error: node.hasError ? node.lastError : undefined,
      notifications: [...node.notifications],
    };
  }

  snapshot(): Record<string, NodeValue> {
    const out: Record<string, NodeValue> = {};
    for (const node of [...this.nodes.values()]) {
      if (node.kind === 'state') {
        out[node.name] = { status: 'ok', value: node.value };
      } else if (node.kind === 'computed') {
        try {
          out[node.name] = { status: 'ok', value: this.readComputed(node) };
        } catch (err) {
          out[node.name] = { status: 'error', ...describeError(err) };
        }
      }
    }
    return out;
  }

  // ---- replay -----------------------------------------------------------

  applyOp(op: Op): void {
    switch (op.op) {
      case 'state':
        this.state(op.name, op.value);
        return;
      case 'computed':
        this.computed(op.name, op.fnId);
        return;
      case 'effect':
        this.effect(op.name, op.fnId);
        return;
      case 'set': {
        const node = this.lookup(op.name);
        if (node.kind !== 'state') throw new Error(`not a state: ${op.name}`);
        this.writeNode(node, op.value);
        this.commit();
        return;
      }
      case 'tx': {
        this.txDepth++;
        try {
          for (const inner of op.ops) {
            if (inner.op !== 'set') throw new Error('only set operations can occur inside a tx log entry');
            const node = this.lookup(inner.name);
            if (node.kind !== 'state') throw new Error(`not a state: ${inner.name}`);
            this.writeNode(node, inner.value);
          }
        } finally {
          this.txDepth--;
        }
        this.commit();
        return;
      }
    }
  }

  // ---- internals --------------------------------------------------------

  private readonly cx: Cx = {
    get: (name: string): unknown => this.readNode(this.lookup(name)),
    has: (name: string): boolean => this.byName.has(name),
    set: (name: string, value: unknown): void => {
      const node = this.lookup(name);
      if (node.kind !== 'state') throw new Error(`cannot write to non-state: ${name}`);
      this.writeNode(node, value);
    },
  };

  private addNode(node: AnyNode): void {
    if (this.byName.has(node.name)) throw new Error(`duplicate node name: ${node.name}`);
    this.nodes.set(node.id, node);
    this.byName.set(node.name, node);
  }

  private lookup(name: string): AnyNode {
    const node = this.byName.get(name);
    if (!node) throw new Error(`unknown node: ${name}`);
    return node;
  }

  private record(op: Op): void {
    if (!this._replaying) this.log.push(op);
  }

  private writeNode(node: StateNode, value: unknown): void {
    if (this.frameBuffers.length > 0) {
      const frame = this.frameBuffers[this.frameBuffers.length - 1]!;
      frame.set(node.id, value);
    } else if (this.flushing) {
      this.deferred.set(node.id, value);
    } else {
      this.staged.set(node.id, value);
    }
  }

  private readNode(node: AnyNode): unknown {
    this.collecting?.add(node);
    if (node.kind === 'state') {
      if (this.observer === null && this.txDepth > 0 && this.staged.has(node.id)) {
        return this.staged.get(node.id);
      }
      return node.value;
    }
    if (node.kind === 'effect') throw new Error(`effects cannot be read: ${node.name}`);
    return this.pull(node);
  }

  private readComputed(node: ComputedNode): unknown {
    this.readDepth++;
    try {
      return this.readNode(node);
    } finally {
      this.readDepth--;
      if (this.readDepth === 0) this.commit();
    }
  }

  private pull(node: ComputedNode): unknown {
    if (!node.dirty) {
      if (node.hasError) throw node.error;
      return node.value;
    }
    const cycleAt = this.computeStack.indexOf(node);
    if (cycleAt !== -1) {
      const chain = [...this.computeStack.slice(cycleAt).map((n) => n.name), node.name];
      throw new CycleError(chain);
    }

    this.computeStack.push(node);
    const previousObserver = this.observer;
    const previousCollecting = this.collecting;
    this.observer = node;
    const fresh = new Set<AnyNode>();
    this.collecting = fresh;
    const frame = new Map<number, unknown>();
    this.frameBuffers.push(frame);
    try {
      const value = this.registry.get(node.fnId)(this.cx);
      node.runs++;
      node.value = value;
      node.hasValue = true;
      node.hasError = false;
      node.error = undefined;
      this.adoptDeps(node, fresh);
      node.dirty = false;
      for (const [id, value2] of frame) this.deferred.set(id, value2);
      return value;
    } catch (err) {
      node.runs++;
      node.hasError = true;
      node.error = err;
      // Adopt the partial read set: the failure outcome depended exactly on
      // those reads, so they are the valid invalidation triggers.
      this.adoptDeps(node, fresh);
      node.dirty = false;
      throw err;
    } finally {
      this.frameBuffers.pop();
      this.computeStack.pop();
      this.observer = previousObserver;
      this.collecting = previousCollecting;
    }
  }

  private adoptDeps(node: ComputedNode | EffectNode, fresh: Set<AnyNode>): void {
    for (const dep of node.deps) {
      if (!fresh.has(dep) && dep.kind !== 'effect') dep.subscribers.delete(node);
    }
    for (const dep of fresh) {
      if (!node.deps.has(dep) && dep.kind !== 'effect') dep.subscribers.add(node);
    }
    node.deps = fresh;
  }

  private markDownstream(changed: StateNode[]): void {
    const stack: (ComputedNode | EffectNode)[] = [];
    for (const state of changed) stack.push(...state.subscribers);
    while (stack.length > 0) {
      const node = stack.pop()!;
      if (node.dirty) continue;
      node.dirty = true;
      if (node.kind === 'computed') stack.push(...node.subscribers);
    }
  }

  private hasPendingWork(): boolean {
    if (this.staged.size > 0 || this.deferred.size > 0) return true;
    for (const node of this.nodes.values()) {
      if (node.kind === 'effect' && node.dirty) return true;
    }
    return false;
  }

  private commit(): void {
    if (this.txDepth > 0 || this.flushing) return;
    if (!this.hasPendingWork()) return;
    this.flushing = true;
    this.commitErrors.length = 0;
    try {
      for (;;) {
        const changed: StateNode[] = [];
        for (const [id, value] of this.staged) {
          const node = this.nodes.get(id);
          if (node && node.kind === 'state' && !Object.is(node.value, value)) {
            node.value = value;
            changed.push(node);
          }
        }
        this.staged.clear();
        this.markDownstream(changed);
        this.runEffects();
        if (this.deferred.size === 0) break;
        this.staged = this.deferred;
        this.deferred = new Map();
      }
    } finally {
      this.flushing = false;
    }
  }

  private runEffects(): void {
    for (const node of this.nodes.values()) {
      if (node.kind !== 'effect' || !node.dirty) continue;
      this.runEffect(node);
    }
  }

  private runEffect(node: EffectNode): void {
    const previousObserver = this.observer;
    const previousCollecting = this.collecting;
    this.observer = node;
    const fresh = new Set<AnyNode>();
    this.collecting = fresh;
    const frame = new Map<number, unknown>();
    this.frameBuffers.push(frame);
    try {
      const output = this.registry.get(node.fnId)(this.cx);
      node.runs++;
      node.lastOutput = output;
      node.hasOutput = true;
      node.lastError = undefined;
      node.hasError = false;
      node.notifications.push(cloneValue(output));
      this.adoptDeps(node, fresh);
      for (const [id, value] of frame) this.deferred.set(id, value);
    } catch (err) {
      node.runs++;
      node.lastError = err;
      node.hasError = true;
      this.adoptDeps(node, fresh);
      this.commitErrors.push({ effect: node.name, error: err });
    } finally {
      this.frameBuffers.pop();
      node.dirty = false;
      this.observer = previousObserver;
      this.collecting = previousCollecting;
    }
  }
}

export function replaySystem(registry: FnRegistry, log: readonly Op[]): ReactiveSystem {
  const system = new ReactiveSystem(registry);
  system._replaying = true;
  for (const op of log) system.applyOp(op);
  system._replaying = false;
  return system;
}
