import {
  cloneJson,
  deepFreeze,
  diffPaths,
  encodePath,
  isObject,
  minimalCover,
  type JsonValue,
  type PathSeg,
} from "./json.ts";
import { ConfigError } from "./errors.ts";
import {
  compileFull,
  compileIncremental,
  isLayerEnabled,
  type CompileResult,
  type CompileStats,
  type Condition,
  type LayerState,
} from "./compile.ts";
import type { ProvenanceEntry } from "./merge.ts";
import { StrategyMap, type StrategyRule } from "./strategy.ts";

export type CommitOp =
  | { kind: "upsertLayer"; layer: { id: string; priority?: number; data: JsonValue; enabled?: boolean; when?: Condition } }
  | { kind: "removeLayer"; id: string }
  | { kind: "setLayerData"; id: string; data: JsonValue }
  | { kind: "setEnabled"; id: string; enabled: boolean }
  | { kind: "swapPriority"; a: string; b: string }
  | { kind: "setContext"; context: JsonValue };

export type Validator = (
  config: JsonValue,
  explain: ReadonlyMap<string, readonly ProvenanceEntry[]>,
) => void;

export interface SnapshotLayer {
  id: string;
  priority: number;
  enabled: boolean;
}

export interface Snapshot {
  readonly version: number;
  readonly config: JsonValue;
  readonly explain: ReadonlyMap<string, readonly ProvenanceEntry[]>;
  readonly layers: readonly SnapshotLayer[];
  readonly context: JsonValue;
  readonly stats: CompileStats;
}

interface Checkpoint {
  layers: LayerState[];
  context: JsonValue;
}

function cloneLayers(layers: readonly LayerState[]): LayerState[] {
  return layers.map((l) => ({ ...l, data: cloneJson(l.data) }));
}

/** Top-level field paths a layer touches (precise enough for dirty tracking). */
function touchedPaths(data: JsonValue): PathSeg[][] {
  if (isObject(data)) return Object.keys(data).map((k) => [k]);
  return [[]];
}

export class ConfigStore {
  private layers: LayerState[] = [];
  private context: JsonValue = {};
  private readonly strategies: StrategyMap;
  private readonly validators: Validator[] = [];
  private compiled: CompileResult;
  private readonly snapshots: Snapshot[] = [];
  private readonly checkpoints: Checkpoint[] = [];
  private readonly subscribers = new Set<(s: Snapshot) => void>();
  private orderCounter = 0;

  constructor(opts: { strategies?: StrategyRule[]; context?: JsonValue; validators?: Validator[] } = {}) {
    this.strategies = new StrategyMap(opts.strategies ?? []);
    this.context = opts.context !== undefined ? cloneJson(opts.context) : {};
    for (const v of opts.validators ?? []) this.validators.push(v);
    this.compiled = compileFull(this.layers, this.context, this.strategies);
    this.snapshots.push(this.makeSnapshot());
  }

  private makeSnapshot(): Snapshot {
    const config = deepFreeze(cloneJson(this.compiled.config));
    const explain = new Map<string, readonly ProvenanceEntry[]>();
    for (const [k, entries] of this.compiled.explain.map) {
      explain.set(k, deepFreeze(entries.map((e) => ({ ...e }))));
    }
    const layers: SnapshotLayer[] = this.layers.map((l) => ({
      id: l.id,
      priority: l.priority,
      enabled: isLayerEnabled(l, this.context),
    }));
    return deepFreeze({
      version: this.snapshots.length,
      config,
      explain,
      layers,
      context: deepFreeze(cloneJson(this.context)),
      stats: { ...this.compiled.stats, recomputedPaths: [...this.compiled.stats.recomputedPaths], resolvedRefs: [...this.compiled.stats.resolvedRefs] },
    });
  }

  /** Atomic publish: the immutable snapshot (config + explain index) is swapped
   *  in one assignment, then subscribers are notified with that exact object. */
  private publish(): Snapshot {
    const snap = this.makeSnapshot();
    this.snapshots.push(snap);
    for (const fn of this.subscribers) {
      try {
        fn(snap);
      } catch {
        // a broken subscriber must not corrupt the store or other subscribers
      }
    }
    return snap;
  }

  subscribe(fn: (s: Snapshot) => void): () => void {
    this.subscribers.add(fn);
    return () => this.subscribers.delete(fn);
  }

  addValidator(v: Validator): void {
    this.validators.push(v);
  }

  getSnapshot(): Snapshot;
  /** Historical snapshots stay available for auditing/diffing. */
  getSnapshot(version: number): Snapshot | undefined;
  getSnapshot(version?: number): Snapshot | undefined {
    return version === undefined ? this.snapshots[this.snapshots.length - 1] : this.snapshots[version];
  }

  getStats(): CompileStats {
    return this.compiled.stats;
  }

  explainAt(path: readonly PathSeg[]): readonly ProvenanceEntry[] {
    return this.getSnapshot().explain.get(encodePath(path)) ?? [];
  }

  getLayerStates(): LayerState[] {
    return cloneLayers(this.layers);
  }

  getContext(): JsonValue {
    return cloneJson(this.context);
  }

  private requireLayer(id: string): LayerState {
    const l = this.layers.find((x) => x.id === id);
    if (!l) throw new ConfigError("INVALID_OP", `unknown layer "${id}"`, { layer: id });
    return l;
  }

  /** Apply a batch of ops transactionally: the new configuration is fully
   *  compiled and validated first; any error rejects the whole batch and the
   *  previous configuration keeps serving. */
  commit(ops: CommitOp | CommitOp[]): Snapshot {
    const opList = Array.isArray(ops) ? ops : [ops];
    const prevLayers = this.layers;
    const prevContext = this.context;
    const prevCompiled = this.compiled;

    this.layers = cloneLayers(prevLayers);
    this.context = cloneJson(prevContext);
    const dirty: PathSeg[][] = [];

    try {
      for (const op of opList) this.applyOp(op, dirty);
      const roots = minimalCover(dirty);
      this.compiled =
        roots.length === 0
          ? prevCompiled
          : compileIncremental(prevCompiled, this.layers, this.context, this.strategies, roots);
      for (const v of this.validators) v(this.compiled.config, this.compiled.explain.map);
    } catch (err) {
      this.layers = prevLayers;
      this.context = prevContext;
      this.compiled = prevCompiled;
      throw err;
    }

    this.checkpoints.push({ layers: prevLayers, context: prevContext });
    return this.publish();
  }

  private applyOp(op: CommitOp, dirty: PathSeg[][]): void {
    switch (op.kind) {
      case "upsertLayer": {
        const existing = this.layers.find((l) => l.id === op.layer.id);
        const data = cloneJson(op.layer.data);
        if (!existing) {
          const maxPriority = this.layers.reduce((m, l) => Math.max(m, l.priority), -10);
          this.layers.push({
            id: op.layer.id,
            priority: op.layer.priority ?? maxPriority + 10,
            data,
            enabled: op.layer.enabled ?? true,
            when: op.layer.when,
            order: this.orderCounter++,
          });
          dirty.push(...touchedPaths(data));
          return;
        }
        const shapeChanged =
          (op.layer.priority !== undefined && op.layer.priority !== existing.priority) ||
          (op.layer.enabled !== undefined && op.layer.enabled !== existing.enabled) ||
          (op.layer.when !== undefined && op.layer.when !== existing.when);
        if (shapeChanged) {
          dirty.push(...touchedPaths(existing.data), ...touchedPaths(data));
        } else {
          dirty.push(...diffPaths(existing.data, data));
        }
        existing.data = data;
        if (op.layer.priority !== undefined) existing.priority = op.layer.priority;
        if (op.layer.enabled !== undefined) existing.enabled = op.layer.enabled;
        if (op.layer.when !== undefined) existing.when = op.layer.when;
        return;
      }
      case "removeLayer": {
        const l = this.requireLayer(op.id);
        dirty.push(...touchedPaths(l.data));
        this.layers = this.layers.filter((x) => x.id !== op.id);
        return;
      }
      case "setLayerData": {
        const l = this.requireLayer(op.id);
        const data = cloneJson(op.data);
        dirty.push(...diffPaths(l.data, data));
        l.data = data;
        return;
      }
      case "setEnabled": {
        const l = this.requireLayer(op.id);
        if (l.enabled !== op.enabled) {
          dirty.push(...touchedPaths(l.data));
          l.enabled = op.enabled;
        }
        return;
      }
      case "swapPriority": {
        const a = this.requireLayer(op.a);
        const b = this.requireLayer(op.b);
        if (a.priority !== b.priority) {
          const t = a.priority;
          a.priority = b.priority;
          b.priority = t;
          dirty.push(...touchedPaths(a.data), ...touchedPaths(b.data));
        }
        return;
      }
      case "setContext": {
        const next = cloneJson(op.context);
        for (const l of this.layers) {
          if (l.when === undefined) continue;
          const before = isLayerEnabled(l, this.context);
          const after = isLayerEnabled(l, next);
          if (before !== after) dirty.push(...touchedPaths(l.data));
        }
        this.context = next;
        return;
      }
    }
  }

  /** Restore the layer state from before the most recent commit and recompile. */
  rollback(): Snapshot {
    const cp = this.checkpoints.pop();
    if (!cp) return this.getSnapshot();
    this.layers = cp.layers;
    this.context = cp.context;
    this.compiled = compileFull(this.layers, this.context, this.strategies);
    for (const v of this.validators) v(this.compiled.config, this.compiled.explain.map);
    return this.publish();
  }
}
