// Independent reference model.
//
// Unlike the incremental engine, this model tracks NO dependency graph:
// after every committed transaction round it recomputes EVERY computed node
// from scratch (via lazy DFS with a per-round memo and an on-stack cycle
// detector) and re-runs EVERY effect. It is deliberately written without
// looking at the engine's invalidation logic, so test equivalence is a real
// cross-check rather than two copies of the same algorithm.

import {
  CycleError,
  describeError,
  FnRegistry,
  type Cx,
  type NodeValue,
} from './core.ts';

interface MComputed {
  name: string;
  fnId: string;
  value: unknown;
  hasValue: boolean;
  error: unknown;
  hasError: boolean;
  runs: number;
}

interface MEffect {
  name: string;
  fnId: string;
  lastOutput: unknown;
  hasOutput: boolean;
  lastError: unknown;
  hasError: boolean;
  runs: number;
  notifications: unknown[];
}

type MemoEntry = { status: 'ok'; value: unknown } | { status: 'error'; error: unknown };

export class ModelSystem {
  private readonly states = new Map<string, unknown>();
  private readonly computeds = new Map<string, MComputed>();
  private readonly effects = new Map<string, MEffect>();
  private readonly computedOrder: string[] = [];
  private readonly effectOrder: string[] = [];

  private txDepth = 0;
  private flushing = false;
  private staged = new Map<string, unknown>();
  private deferred = new Map<string, unknown>();
  private readonly frameBuffers: Map<string, unknown>[] = [];

  private memo = new Map<string, MemoEntry>();
  private readonly stack: string[] = [];

  readonly commitErrors: { effect: string; error: unknown }[] = [];

  private readonly registry: FnRegistry;

  constructor(registry: FnRegistry) {
    this.registry = registry;
  }

  state(name: string, value: unknown): void {
    this.states.set(name, value);
  }

  computed(name: string, fnId: string): void {
    this.computeds.set(name, {
      name,
      fnId,
      value: undefined,
      hasValue: false,
      error: undefined,
      hasError: false,
      runs: 0,
    });
    this.computedOrder.push(name);
  }

  effect(name: string, fnId: string): void {
    this.effects.set(name, {
      name,
      fnId,
      lastOutput: undefined,
      hasOutput: false,
      lastError: undefined,
      hasError: false,
      runs: 0,
      notifications: [],
    });
    this.effectOrder.push(name);
    this.flush();
  }

  set(name: string, value: unknown): void {
    this.write(name, value);
    this.flush();
  }

  tx(fn: () => void): void {
    this.txDepth++;
    try {
      fn();
    } finally {
      this.txDepth--;
    }
    this.flush();
  }

  stats(): Record<string, number> {
    const out: Record<string, number> = {};
    for (const c of this.computeds.values()) out[c.name] = c.runs;
    for (const e of this.effects.values()) out[e.name] = e.runs;
    return out;
  }

  effectInfo(name: string): {
    runs: number;
    lastOutput: unknown;
    error: unknown;
    notifications: unknown[];
  } {
    const node = this.effects.get(name);
    if (!node) throw new Error(`not an effect: ${name}`);
    return {
      runs: node.runs,
      lastOutput: node.lastOutput,
      error: node.hasError ? node.lastError : undefined,
      notifications: [...node.notifications],
    };
  }

  snapshot(): Record<string, NodeValue> {
    const out: Record<string, NodeValue> = {};
    for (const name of this.states.keys()) {
      out[name] = { status: 'ok', value: this.states.get(name) };
    }
    for (const name of this.computedOrder) {
      try {
        out[name] = { status: 'ok', value: this.visit(name) };
      } catch (err) {
        out[name] = { status: 'error', ...describeError(err) };
      }
    }
    return out;
  }

  private readonly cx: Cx = {
    get: (name: string): unknown => this.readByName(name),
    has: (name: string): boolean => this.computeds.has(name) || this.states.has(name),
    set: (name: string, value: unknown): void => this.write(name, value),
  };

  private readByName(name: string): unknown {
    if (this.states.has(name)) return this.states.get(name);
    if (this.computeds.has(name)) return this.visit(name);
    throw new Error(`unknown node: ${name}`);
  }

  private write(name: string, value: unknown): void {
    if (this.frameBuffers.length > 0) {
      const frame = this.frameBuffers[this.frameBuffers.length - 1]!;
      frame.set(name, value);
    } else if (this.flushing) {
      this.deferred.set(name, value);
    } else {
      this.staged.set(name, value);
    }
  }

  private visit(name: string): unknown {
    const node = this.computeds.get(name)!;
    const cached = this.memo.get(name);
    if (cached) {
      if (cached.status === 'error') throw cached.error;
      return cached.value;
    }
    const cycleAt = this.stack.indexOf(name);
    if (cycleAt !== -1) {
      throw new CycleError([...this.stack.slice(cycleAt), name]);
    }
    this.stack.push(name);
    const frame = new Map<string, unknown>();
    this.frameBuffers.push(frame);
    try {
      const value = this.registry.get(node.fnId)(this.cx);
      node.runs++;
      node.value = value;
      node.hasValue = true;
      node.hasError = false;
      node.error = undefined;
      this.memo.set(name, { status: 'ok', value });
      for (const [stateName, stateValue] of frame) this.deferred.set(stateName, stateValue);
      return value;
    } catch (err) {
      node.runs++;
      node.hasError = true;
      node.error = err;
      this.memo.set(name, { status: 'error', error: err });
      throw err;
    } finally {
      this.frameBuffers.pop();
      this.stack.pop();
    }
  }

  private flush(): void {
    if (this.txDepth > 0 || this.flushing) return;
    this.flushing = true;
    this.commitErrors.length = 0;
    try {
      for (;;) {
        for (const [name, value] of this.staged) this.states.set(name, value);
        this.staged.clear();

        // Full recomputation: nothing is cached across rounds.
        this.memo = new Map();

        for (const name of this.effectOrder) this.runEffect(name);
        for (const name of this.computedOrder) {
          try {
            this.visit(name);
          } catch {
            // Error is stored in the node; snapshot comparison observes it.
          }
        }

        if (this.deferred.size === 0) break;
        this.staged = this.deferred;
        this.deferred = new Map();
      }
    } finally {
      this.flushing = false;
    }
  }

  private runEffect(name: string): void {
    const node = this.effects.get(name)!;
    const frame = new Map<string, unknown>();
    this.frameBuffers.push(frame);
    try {
      const output = this.registry.get(node.fnId)(this.cx);
      node.runs++;
      node.lastOutput = output;
      node.hasOutput = true;
      node.lastError = undefined;
      node.hasError = false;
      node.notifications.push(output);
      for (const [stateName, stateValue] of frame) this.deferred.set(stateName, stateValue);
    } catch (err) {
      node.runs++;
      node.lastError = err;
      node.hasError = true;
      this.commitErrors.push({ effect: node.name, error: err });
    } finally {
      this.frameBuffers.pop();
    }
  }
}
