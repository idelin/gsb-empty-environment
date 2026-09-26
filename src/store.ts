import { deepClone, deepFreeze, isPlainObject, pathToPointer } from "./json.ts";
import type { JsonObject, JsonValue, Path } from "./json.ts";
import { compileFromScratch, hotUpdateLayer } from "./compiler.ts";
import type { ActiveLayer, CompiledState } from "./compiler.ts";
import { StrategyMap } from "./strategy.ts";
import type { StrategyRule } from "./strategy.ts";
import type { ExplainRecord } from "./merge.ts";

/** A configuration layer. `enabledWhen` gates the layer on the context. */
export interface LayerDef {
  readonly id: string;
  readonly data: JsonObject;
  readonly enabledWhen?: (context: JsonObject) => boolean;
}

/**
 * An immutable, atomically published view: resolved config + explain index +
 * dependency graph always move together. Safe to read concurrently and to
 * keep as a historical snapshot.
 */
export interface Snapshot {
  readonly version: number;
  readonly config: JsonValue;
  readonly explain: Readonly<Record<string, ExplainRecord>>;
  readonly dependencies: Readonly<Record<string, string>>;
  readonly layers: readonly LayerDef[];
  readonly context: JsonObject;
  /** Pointers recomputed by this publish ("<all>" for full compiles). */
  readonly recomputed: readonly string[];
  readonly publishedAt: number;
}

export type Subscriber = (snapshot: Snapshot) => void;

const ALL: readonly string[] = Object.freeze(["<all>"]);

export interface StoreOptions {
  readonly strategies?: readonly StrategyRule[];
  readonly context?: JsonObject;
}

/**
 * Layered configuration store. Layers sit in a priority list (index 0 is the
 * lowest priority). Every mutating operation validates and compiles first,
 * then swaps one immutable snapshot object, so a failed commit never
 * disturbs the configuration readers currently see.
 */
export class LayeredConfigStore {
  private readonly strategies: StrategyMap;
  private layers: LayerDef[];
  private context: JsonObject;
  private state: CompiledState;
  private current: Snapshot;
  private readonly history = new Map<number, Snapshot>();
  private readonly subscribers = new Set<Subscriber>();

  constructor(options: StoreOptions = {}) {
    this.strategies = new StrategyMap(options.strategies ?? []);
    this.layers = [];
    this.context = deepFreeze(deepClone(options.context ?? {}));
    this.state = compileFromScratch([], this.strategies);
    this.current = this.buildSnapshot(0, ALL);
    this.history.set(0, this.current);
  }

  // ---------------------------------------------------------------- reads

  /** The current consistent snapshot (config + explain + dependencies). */
  snapshot(): Snapshot {
    return this.current;
  }

  /** A historical snapshot by version, undefined when unknown. */
  snapshotAt(version: number): Snapshot | undefined {
    return this.history.get(version);
  }

  versions(): number[] {
    return [...this.history.keys()];
  }

  explainAt(path: Path | string): ExplainRecord | undefined {
    const pointer = typeof path === "string" ? path : pathToPointer(path);
    return this.current.explain[pointer];
  }

  listLayers(): Array<{ id: string; enabled: boolean }> {
    return this.layers.map((l) => ({ id: l.id, enabled: this.isEnabled(l) }));
  }

  subscribe(subscriber: Subscriber): () => void {
    this.subscribers.add(subscriber);
    return () => this.subscribers.delete(subscriber);
  }

  // ------------------------------------------------------------- mutations

  addLayer(def: LayerDef, index?: number): Snapshot {
    this.validateLayerDef(def);
    if (this.layers.some((l) => l.id === def.id)) {
      throw new Error(`duplicate layer id ${JSON.stringify(def.id)}`);
    }
    const frozen = this.freezeDef(def);
    const layers = [...this.layers];
    const at = index === undefined ? layers.length : index;
    if (at < 0 || at > layers.length) throw new RangeError("layer index out of range");
    layers.splice(at, 0, frozen);
    return this.commitWithLayers(layers, this.context);
  }

  removeLayer(id: string): Snapshot {
    const layers = this.layers.filter((l) => l.id !== id);
    if (layers.length === this.layers.length) {
      throw new Error(`unknown layer ${JSON.stringify(id)}`);
    }
    return this.commitWithLayers(layers, this.context);
  }

  /** Hot update of one layer's data: recomputes only affected fields. */
  updateLayerData(id: string, data: JsonObject): Snapshot {
    const idx = this.layers.findIndex((l) => l.id === id);
    if (idx < 0) throw new Error(`unknown layer ${JSON.stringify(id)}`);
    if (!isPlainObject(data)) {
      throw new TypeError(`layer data must be a JSON object (layer ${JSON.stringify(id)})`);
    }
    const def = this.layers[idx];
    const frozen = this.freezeDef({ ...def, data });
    if (!this.isEnabled(def)) {
      // Inactive layer: its data cannot affect the merged config.
      const layers = [...this.layers];
      layers[idx] = frozen;
      this.layers = layers;
      return this.current;
    }
    // Compiles on copies; throws before anything is mutated on failure.
    const result = hotUpdateLayer(this.state, this.strategies, id, frozen.data);
    const layers = [...this.layers];
    layers[idx] = frozen;
    if (result === null) {
      this.layers = layers;
      return this.current;
    }
    this.layers = layers;
    this.state = result.state;
    return this.publish(result.recomputed);
  }

  /** Swaps the priority of two layers and recompiles from scratch. */
  swapLayers(idA: string, idB: string): Snapshot {
    const a = this.layers.findIndex((l) => l.id === idA);
    const b = this.layers.findIndex((l) => l.id === idB);
    if (a < 0) throw new Error(`unknown layer ${JSON.stringify(idA)}`);
    if (b < 0) throw new Error(`unknown layer ${JSON.stringify(idB)}`);
    const layers = [...this.layers];
    [layers[a], layers[b]] = [layers[b], layers[a]];
    return this.commitWithLayers(layers, this.context);
  }

  /** Replaces the context, re-evaluates layer conditions, recompiles. */
  setContext(context: JsonObject): Snapshot {
    return this.commitWithLayers(this.layers, deepFreeze(deepClone(context)));
  }

  /**
   * Rolls back to a historical version: restores that version's layer set
   * and context, then recompiles from scratch and publishes a new version
   * (history itself is append-only).
   */
  rollback(version: number): Snapshot {
    const snap = this.history.get(version);
    if (snap === undefined) {
      throw new Error(`unknown version ${version}`);
    }
    return this.commitWithLayers([...snap.layers], deepClone(snap.context));
  }

  // -------------------------------------------------------------- internal

  private isEnabled(def: LayerDef): boolean {
    return def.enabledWhen === undefined || def.enabledWhen(this.context);
  }

  private activeLayers(layers: readonly LayerDef[], context: JsonObject): ActiveLayer[] {
    return layers
      .filter((l) => l.enabledWhen === undefined || l.enabledWhen(context))
      .map((l) => ({ id: l.id, data: l.data }));
  }

  private validateLayerDef(def: LayerDef): void {
    if (typeof def.id !== "string" || def.id === "") {
      throw new TypeError("layer id must be a non-empty string");
    }
    if (!isPlainObject(def.data)) {
      throw new TypeError(`layer data must be a JSON object (layer ${JSON.stringify(def.id)})`);
    }
    if (def.enabledWhen !== undefined && typeof def.enabledWhen !== "function") {
      throw new TypeError(`enabledWhen must be a function (layer ${JSON.stringify(def.id)})`);
    }
  }

  private freezeDef(def: LayerDef): LayerDef {
    return Object.freeze({
      id: def.id,
      data: deepFreeze(deepClone(def.data)),
      enabledWhen: def.enabledWhen,
    });
  }

  /** Full recompile + atomic publish. On any compile error nothing mutates. */
  private commitWithLayers(layers: readonly LayerDef[], context: JsonObject): Snapshot {
    const state = compileFromScratch(this.activeLayers(layers, context), this.strategies);
    this.layers = [...layers];
    this.context = context;
    this.state = state;
    return this.publish(ALL);
  }

  private buildSnapshot(version: number, recomputed: readonly string[]): Snapshot {
    const snap: Snapshot = {
      version,
      config: deepFreeze(this.state.config),
      explain: Object.freeze({ ...this.state.explain }),
      dependencies: Object.freeze({ ...this.state.dependencies }),
      layers: Object.freeze([...this.layers]),
      context: deepFreeze(deepClone(this.context)),
      recomputed: Object.freeze([...recomputed]),
      publishedAt: Date.now(),
    };
    return Object.freeze(snap);
  }

  /** Atomic swap: one immutable object carries config + explain + graph. */
  private publish(recomputed: readonly string[]): Snapshot {
    const snap = this.buildSnapshot(this.current.version + 1, recomputed);
    this.current = snap;
    this.history.set(snap.version, snap);
    for (const sub of [...this.subscribers]) sub(snap);
    return snap;
  }
}
