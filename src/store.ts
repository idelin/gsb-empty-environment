import { GraphError } from './errors.ts';
import {
  assertNoEvaluationCycles,
  isComputed,
  type ComputedNode,
  type GraphNode,
} from './graph.ts';
import type { DefinitionRegistry } from './registry.ts';

export interface SetOp {
  op: 'set';
  id: string;
  value: unknown;
}

export type NodeSpec =
  | { kind: 'cell'; id: string; initial: unknown }
  | {
      kind: 'computed';
      id: string;
      definition: { id: string; version: number };
      deps: string[];
      monotonic?: boolean;
    };

export type LogEntry =
  | { seq: number; kind: 'define-cell'; id: string; initial: unknown }
  | {
      seq: number;
      kind: 'define-computed';
      id: string;
      definition: { id: string; version: number };
      deps: string[];
      monotonic: boolean;
    }
  | { seq: number; kind: 'tx'; ops: SetOp[] }
  | { seq: number; kind: 'restore'; envelope: unknown };

export interface Epoch {
  readonly seq: number;
  readonly values: ReadonlyMap<string, unknown>;
}

export type Subscriber = (value: unknown, id: string) => void;

const MAX_RING_ITERATIONS = 64;

export class Store {
  private nodes = new Map<string, GraphNode>();
  private epoch: Epoch = { seq: 0, values: new Map() };
  private readonly subscribers = new Map<string, Set<Subscriber>>();
  readonly log: LogEntry[] = [];

  private readonly registry: DefinitionRegistry;

  constructor(registry: DefinitionRegistry) {
    this.registry = registry;
  }

  // --- graph definition -------------------------------------------------

  defineCell(id: string, initial: unknown): void {
    if (this.nodes.has(id)) {
      throw new GraphError('DUPLICATE_NODE', `node already defined: ${id}`);
    }
    this.nodes.set(id, { kind: 'cell', id });
    const values = new Map(this.epoch.values);
    values.set(id, initial);
    this.epoch = { seq: this.epoch.seq + 1, values };
    this.log.push({ seq: this.epoch.seq, kind: 'define-cell', id, initial });
  }

  defineComputed(
    id: string,
    definition: { id: string; version: number },
    deps: string[],
    opts: { monotonic?: boolean } = {},
  ): void {
    if (this.nodes.has(id)) {
      throw new GraphError('DUPLICATE_NODE', `node already defined: ${id}`);
    }
    const def = this.registry.resolve(definition.id, definition.version);
    if (!def) {
      throw new GraphError(
        'UNKNOWN_DEFINITION',
        `unknown computation definition: ${definition.id}@${definition.version}`,
      );
    }
    for (const dep of deps) {
      if (!this.nodes.has(dep)) {
        throw new GraphError('MISSING_NODE', `dependency not defined: ${dep}`);
      }
    }
    const node: ComputedNode = {
      kind: 'computed',
      id,
      definitionId: definition.id,
      definitionVersion: definition.version,
      deps: [...deps],
      monotonic: opts.monotonic ?? false,
    };
    this.nodes.set(id, node);
    try {
      assertNoEvaluationCycles(this.nodes);
    } catch (err) {
      this.nodes.delete(id);
      throw err;
    }
    const values = new Map(this.epoch.values);
    values.set(id, this.evaluate(node, values, undefined));
    this.epoch = { seq: this.epoch.seq + 1, values };
    this.log.push({
      seq: this.epoch.seq,
      kind: 'define-computed',
      id,
      definition: { ...definition },
      deps: [...deps],
      monotonic: node.monotonic,
    });
  }

  /**
   * Atomically define a batch of nodes (supports forward references, e.g.
   * monotonic rings). Validates and evaluates everything before publishing;
   * on failure the store is left untouched.
   */
  defineGraph(specs: NodeSpec[]): void {
    const nodes = new Map(this.nodes);
    const values = new Map(this.epoch.values);
    const computedIds: string[] = [];
    for (const spec of specs) {
      if (nodes.has(spec.id)) {
        throw new GraphError('DUPLICATE_NODE', `node already defined: ${spec.id}`);
      }
      if (spec.kind === 'cell') {
        nodes.set(spec.id, { kind: 'cell', id: spec.id });
        values.set(spec.id, spec.initial);
        continue;
      }
      const def = this.registry.resolve(spec.definition.id, spec.definition.version);
      if (!def) {
        throw new GraphError(
          'UNKNOWN_DEFINITION',
          `unknown computation definition: ${spec.definition.id}@${spec.definition.version}`,
        );
      }
      nodes.set(spec.id, {
        kind: 'computed',
        id: spec.id,
        definitionId: spec.definition.id,
        definitionVersion: spec.definition.version,
        deps: [...spec.deps],
        monotonic: spec.monotonic ?? false,
      });
      computedIds.push(spec.id);
    }
    for (const node of nodes.values()) {
      if (!isComputed(node)) continue;
      for (const dep of node.deps) {
        if (!nodes.has(dep)) {
          throw new GraphError('MISSING_NODE', `dependency not defined: ${dep}`);
        }
      }
    }
    assertNoEvaluationCycles(nodes);

    // Publish graph first so evaluation can resolve nodes, then compute values.
    const previousNodes = this.nodes;
    const previousEpoch = this.epoch;
    this.nodes = nodes;
    this.epoch = { seq: this.epoch.seq + 1, values };
    try {
      this.recomputeAll(computedIds);
    } catch (err) {
      this.nodes = previousNodes;
      this.epoch = previousEpoch;
      throw err;
    }
    for (const spec of specs) {
      if (spec.kind === 'cell') {
        this.log.push({ seq: this.epoch.seq, kind: 'define-cell', id: spec.id, initial: spec.initial });
      } else {
        this.log.push({
          seq: this.epoch.seq,
          kind: 'define-computed',
          id: spec.id,
          definition: { ...spec.definition },
          deps: [...spec.deps],
          monotonic: spec.monotonic ?? false,
        });
      }
    }
  }

  // --- reading ------------------------------------------------------------

  node(id: string): GraphNode {
    const node = this.nodes.get(id);
    if (!node) throw new GraphError('UNKNOWN_NODE', `no such node: ${id}`);
    return node;
  }

  hasNode(id: string): boolean {
    return this.nodes.has(id);
  }

  /** Snapshot of committed state; holders never observe transaction intermediates. */
  snapshot(): Epoch {
    return this.epoch;
  }

  read(id: string, epoch: Epoch = this.epoch): unknown {
    if (!this.nodes.has(id)) {
      throw new GraphError('UNKNOWN_NODE', `no such node: ${id}`);
    }
    return epoch.values.get(id);
  }

  // --- transactions -------------------------------------------------------

  beginTx(): Tx {
    return new Tx(this);
  }

  /** Atomically publish a batch of set-ops. Throws on failure; state untouched. */
  commitOps(ops: SetOp[]): void {
    const before = this.epoch.values;
    const changed = this.applyOps(ops);
    this.epoch = { seq: this.epoch.seq + 1, values: changed };
    this.log.push({ seq: this.epoch.seq, kind: 'tx', ops });
    this.notify(before, changed);
  }

  private applyOps(ops: SetOp[]): Map<string, unknown> {
    const values = new Map(this.epoch.values);
    for (const op of ops) {
      const node = this.nodes.get(op.id);
      if (!node) throw new GraphError('UNKNOWN_NODE', `no such node: ${op.id}`);
      if (node.kind !== 'cell') {
        throw new GraphError('NOT_A_CELL', `cannot set computed node: ${op.id}`);
      }
      values.set(op.id, op.value);
    }
    this.recomputeDirty(values, ops.map((op) => op.id));
    return values;
  }

  // --- subscriptions --------------------------------------------------------

  subscribe(id: string, cb: Subscriber): () => void {
    if (!this.nodes.has(id)) {
      throw new GraphError('UNKNOWN_NODE', `no such node: ${id}`);
    }
    let set = this.subscribers.get(id);
    if (!set) {
      set = new Set();
      this.subscribers.set(id, set);
    }
    set.add(cb);
    return () => set.delete(cb);
  }

  private notify(before: ReadonlyMap<string, unknown>, after: Map<string, unknown>): void {
    for (const [id, subs] of this.subscribers) {
      if (before.get(id) === after.get(id)) continue;
      for (const cb of subs) cb(after.get(id), id);
    }
  }

  // --- restore (used by serde; atomic, silent) ------------------------------

  /** Replace graph + state in one shot. Never fires subscriber callbacks. */
  install(
    nodes: Map<string, GraphNode>,
    values: Map<string, unknown>,
    entry?: unknown,
  ): void {
    this.nodes = nodes;
    this.epoch = { seq: this.epoch.seq + 1, values };
    if (entry !== undefined) {
      this.log.push({ seq: this.epoch.seq, kind: 'restore', envelope: entry });
    }
  }

  /** Internal view for serialization. */
  graphView(): { nodes: ReadonlyMap<string, GraphNode>; values: ReadonlyMap<string, unknown> } {
    return { nodes: this.nodes, values: this.epoch.values };
  }

  /** Current committed values (used by restore to publish recomputed state). */
  graphValues(): Map<string, unknown> {
    return new Map(this.epoch.values);
  }

  /** Recompute the given computed nodes from current cell values (restore path). */
  recomputeAll(computedIds: string[]): void {
    const values = new Map(this.epoch.values);
    this.evaluateDirty(values, new Set(computedIds));
    this.epoch = { seq: this.epoch.seq + 1, values };
  }

  // --- evaluation -----------------------------------------------------------

  private evaluate(node: ComputedNode, values: Map<string, unknown>, prev: unknown): unknown {
    const def = this.registry.resolve(node.definitionId, node.definitionVersion);
    if (!def) {
      throw new GraphError(
        'UNKNOWN_DEFINITION',
        `unknown computation definition: ${node.definitionId}@${node.definitionVersion}`,
      );
    }
    return def.compute(node.deps.map((dep) => values.get(dep)), prev);
  }

  private dependentsIndex(): Map<string, string[]> {
    const dependents = new Map<string, string[]>();
    for (const node of this.nodes.values()) {
      if (!isComputed(node)) continue;
      for (const dep of node.deps) {
        const list = dependents.get(dep) ?? [];
        list.push(node.id);
        dependents.set(dep, list);
      }
    }
    return dependents;
  }

  private recomputeDirty(values: Map<string, unknown>, changedCells: string[]): void {
    const dependents = this.dependentsIndex();
    const dirty = new Set<string>();
    const queue = [...changedCells];
    while (queue.length > 0) {
      const id = queue.pop()!;
      for (const dependent of dependents.get(id) ?? []) {
        if (!dirty.has(dependent)) {
          dirty.add(dependent);
          queue.push(dependent);
        }
      }
    }
    if (dirty.size === 0) return;
    this.evaluateDirty(values, dirty);
  }

  private evaluateDirty(values: Map<string, unknown>, dirty: Set<string>): void {
    // Acyclic part: Kahn passes in dependency order.
    const evaluated = new Set<string>();
    let progressed = true;
    while (progressed) {
      progressed = false;
      for (const id of dirty) {
        if (evaluated.has(id)) continue;
        const node = this.nodes.get(id)!;
        if (!isComputed(node)) continue;
        const ready = node.deps.every((dep) => !dirty.has(dep) || evaluated.has(dep));
        if (ready) {
          values.set(id, this.evaluate(node, values, this.epoch.values.get(id)));
          evaluated.add(id);
          progressed = true;
        }
      }
    }
    if (evaluated.size === dirty.size) return;

    // Remaining nodes form rings; only monotonic rings are legal (validated at
    // define/restore time, re-checked here before evaluation).
    const remaining = [...dirty].filter((id) => !evaluated.has(id));
    for (const id of remaining) {
      const node = this.nodes.get(id)!;
      if (!isComputed(node) || !node.monotonic) {
        throw new GraphError('EVALUATION_CYCLE', `illegal evaluation cycle at: ${id}`);
      }
    }
    for (let iteration = 0; iteration < MAX_RING_ITERATIONS; iteration += 1) {
      let changed = false;
      for (const id of remaining) {
        const node = this.nodes.get(id)! as ComputedNode;
        const after = this.evaluate(node, values, this.epoch.values.get(id));
        if (!Object.is(values.get(id), after)) {
          values.set(id, after);
          changed = true;
        }
      }
      if (!changed) return;
    }
    throw new GraphError('MONOTONIC_NONCONVERGENCE', 'monotonic ring did not converge');
  }
}

export class Tx {
  private ops: SetOp[] = [];
  private closed = false;

  private readonly store: Store;

  constructor(store: Store) {
    this.store = store;
  }

  set(id: string, value: unknown): this {
    if (this.closed) throw new GraphError('TX_CLOSED', 'transaction already closed');
    this.ops.push({ op: 'set', id, value });
    return this;
  }

  commit(): void {
    if (this.closed) throw new GraphError('TX_CLOSED', 'transaction already closed');
    this.closed = true;
    this.store.commitOps(this.ops);
  }

  rollback(): void {
    this.closed = true;
    this.ops = [];
  }
}
