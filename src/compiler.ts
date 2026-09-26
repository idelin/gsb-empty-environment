import {
  deepEqual,
  deepFreeze,
  getIn,
  isPlainObject,
  isPrefixPath,
  MISSING,
  pathToPointer,
  pathsOverlap,
  pointerToPath,
  pointerUnder,
  setInImmutable,
} from "./json.ts";
import type { JsonObject, JsonValue, Path } from "./json.ts";
import { isDeleteSentinel } from "./sentinel.ts";
import { isRefNode, findRefs, RefResolver } from "./refs.ts";
import { mergeField } from "./merge.ts";
import type { ExplainRecord, RecordSink } from "./merge.ts";
import type { StrategyMap } from "./strategy.ts";

/** A layer that is enabled under the current context. */
export interface ActiveLayer {
  readonly id: string;
  readonly data: JsonObject;
}

/**
 * Internal compiled state. `config`/`explain`/`dependencies` are published
 * (frozen) in snapshots; the rest supports incremental recomputation.
 */
export interface CompiledState {
  readonly layers: readonly ActiveLayer[];
  readonly raw: JsonValue;
  readonly config: JsonValue;
  readonly explain: Record<string, ExplainRecord>;
  /** Compiled reference dependency graph: ref location pointer -> target pointer. */
  readonly dependencies: Record<string, string>;
  readonly resolvedRefs: ReadonlyMap<string, JsonValue>;
  readonly resolvedTargets: ReadonlyMap<string, JsonValue>;
}

function makeRecord(
  path: Path,
  strategy: "override" | "deep" | "byId",
  contributions: ExplainRecord["contributions"],
  winner: string | undefined,
  present: boolean,
  value: JsonValue | undefined,
  resolvedFrom: string | undefined,
): ExplainRecord {
  return deepFreeze({
    pointer: pathToPointer(path),
    path: [...path],
    strategy,
    present,
    value,
    winner,
    contributions,
    resolvedFrom,
  });
}

function makeSink(
  explain: Record<string, ExplainRecord>,
  strategies: StrategyMap,
): RecordSink {
  return (path, contributions, winner, present, value) => {
    explain[pathToPointer(path)] = makeRecord(
      path,
      strategies.strategyAt(path).kind,
      deepFreeze([...contributions]),
      winner,
      present,
      value,
      undefined,
    );
  };
}

/** Stamps resolved values and ref links onto the explain records of refs. */
function applyRefExplanations(
  explain: Record<string, ExplainRecord>,
  dependencies: Record<string, string>,
  resolvedRefs: ReadonlyMap<string, JsonValue>,
  only?: ReadonlySet<string>,
): void {
  for (const [refPointer, targetPointer] of Object.entries(dependencies)) {
    if (only !== undefined && !only.has(refPointer)) continue;
    const prev = explain[refPointer];
    const path = pointerToPath(refPointer);
    explain[refPointer] = makeRecord(
      path,
      prev?.strategy ?? "deep",
      prev?.contributions ?? [],
      prev?.winner,
      true,
      resolvedRefs.get(refPointer),
      targetPointer,
    );
  }
}

/** Full from-scratch compile: merge every layer, compile the reference
 *  dependency graph, detect cycles and resolve all references. */
export function compileFromScratch(
  layers: readonly ActiveLayer[],
  strategies: StrategyMap,
): CompiledState {
  const explain: Record<string, ExplainRecord> = {};
  const sink = makeSink(explain, strategies);
  const entries = layers.map((l) => ({
    layerId: l.id,
    present: true,
    value: l.data as JsonValue,
  }));
  const merged = mergeField(entries, [], strategies, sink);
  const raw: JsonValue = merged.present ? (merged.value as JsonValue) : {};

  const dependencies: Record<string, string> = {};
  for (const [refPointer, targetPointer] of findRefs(raw, [])) {
    dependencies[refPointer] = targetPointer;
  }

  const resolvedRefs = new Map<string, JsonValue>();
  const resolvedTargets = new Map<string, JsonValue>();
  const resolver = new RefResolver(raw, resolvedRefs, resolvedTargets);
  const config = resolver.resolveSubtree(raw, []); // throws on cycles/bad refs

  applyRefExplanations(explain, dependencies, resolvedRefs);
  return { layers, raw, config, explain, dependencies, resolvedRefs, resolvedTargets };
}

/** Strategy-aware structural diff: emits the minimal merge-unit paths whose
 *  recomputation covers every difference between two layer trees. */
export function diffTrees(
  oldV: JsonValue | typeof MISSING,
  newV: JsonValue | typeof MISSING,
  path: Path,
  strategies: StrategyMap,
  out: Path[],
): void {
  if (oldV === MISSING || newV === MISSING) {
    if (oldV !== newV) out.push(path);
    return;
  }
  const bothObjects = isMergeableObject(oldV) && isMergeableObject(newV);
  if (bothObjects && strategies.strategyAt(path).kind === "deep") {
    const keys = new Set([
      ...Object.keys(oldV as JsonObject),
      ...Object.keys(newV as JsonObject),
    ]);
    for (const k of keys) {
      const a = oldV as JsonObject;
      const b = newV as JsonObject;
      diffTrees(
        Object.hasOwn(a, k) ? a[k] : MISSING,
        Object.hasOwn(b, k) ? b[k] : MISSING,
        [...path, k],
        strategies,
        out,
      );
    }
    return;
  }
  if (!deepEqual(oldV, newV)) out.push(path);
}

function isMergeableObject(v: unknown): v is JsonObject {
  return isPlainObject(v) && !isDeleteSentinel(v) && !isRefNode(v);
}

function invalidateCaches(
  resolvedRefs: Map<string, JsonValue>,
  resolvedTargets: Map<string, JsonValue>,
  paths: readonly Path[],
): void {
  for (const map of [resolvedRefs, resolvedTargets]) {
    for (const key of [...map.keys()]) {
      if (paths.some((p) => pathsOverlap(p, pointerToPath(key)))) {
        map.delete(key);
      }
    }
  }
}

export interface HotUpdateResult {
  readonly state: CompiledState;
  /** Pointers of the fields that were actually recomputed. */
  readonly recomputed: readonly string[];
}

/**
 * Incremental single-layer update. Recomputes only the merge units that
 * changed in this layer plus every reference field that (transitively)
 * depends on them. Works entirely on copies: if validation or cycle
 * detection throws, `prev` is untouched and the old config stays usable.
 *
 * Returns null when the layer's effective tree did not change.
 */
export function hotUpdateLayer(
  prev: CompiledState,
  strategies: StrategyMap,
  layerId: string,
  newData: JsonObject,
): HotUpdateResult | null {
  const oldLayer = prev.layers.find((l) => l.id === layerId);
  if (oldLayer === undefined) {
    throw new Error(`layer ${JSON.stringify(layerId)} is not active`);
  }
  const units: Path[] = [];
  diffTrees(oldLayer.data, newData, [], strategies, units);
  if (units.length === 0) return null;

  const layers = prev.layers.map((l) =>
    l.id === layerId ? { id: l.id, data: newData } : l,
  );
  const explain: Record<string, ExplainRecord> = { ...prev.explain };
  const dependencies: Record<string, string> = { ...prev.dependencies };
  const resolvedRefs = new Map(prev.resolvedRefs);
  const resolvedTargets = new Map(prev.resolvedTargets);
  let raw = prev.raw;
  let config = prev.config;
  const recomputed = new Set<string>();
  const sink = makeSink(explain, strategies);

  // 1. Re-merge only the changed units across all layers.
  for (const unit of units) {
    const pointer = pathToPointer(unit);
    for (const key of Object.keys(explain)) {
      if (pointerUnder(key, pointer)) delete explain[key];
    }
    const entries = layers.map((l) => {
      const v = getIn(l.data, unit);
      return {
        layerId: l.id,
        present: v !== MISSING,
        value: v === MISSING ? undefined : v,
      };
    });
    const merged = mergeField(entries, unit, strategies, sink);
    raw = setInImmutable(raw, unit, merged.present ? (merged.value as JsonValue) : MISSING);
    recomputed.add(pointer === "" ? "/" : pointer);
  }

  // 2. Recompile the dependency graph edges below the changed units.
  for (const unit of units) {
    const pointer = pathToPointer(unit);
    for (const key of Object.keys(dependencies)) {
      if (pointerUnder(key, pointer)) delete dependencies[key];
    }
    const sub = getIn(raw, unit);
    if (sub !== MISSING) {
      for (const [refPointer, targetPointer] of findRefs(sub, unit)) {
        dependencies[refPointer] = targetPointer;
      }
    }
  }

  // 3. Invalidate cached resolutions overlapping the changed units.
  const changed: Path[] = [...units];
  invalidateCaches(resolvedRefs, resolvedTargets, changed);

  // 4. Fixpoint: every ref whose target overlaps a changed path is affected,
  //    plus every ref located inside a changed unit.
  const affected = new Set<string>();
  let grew = true;
  while (grew) {
    grew = false;
    for (const [refPointer, targetPointer] of Object.entries(dependencies)) {
      if (affected.has(refPointer)) continue;
      const refPath = pointerToPath(refPointer);
      const targetPath = pointerToPath(targetPointer);
      const insideUnit = units.some((u) => isPrefixPath(u, refPath));
      const targetHit = changed.some((c) => pathsOverlap(c, targetPath));
      if (insideUnit || targetHit) {
        affected.add(refPointer);
        changed.push(refPath);
        grew = true;
      }
    }
  }
  invalidateCaches(resolvedRefs, resolvedTargets, [...affected].map(pointerToPath));

  // 5. Re-resolve: changed units wholesale, then affected refs elsewhere.
  //    Cycle detection happens here; a throw discards all of the above.
  const resolver = new RefResolver(raw, resolvedRefs, resolvedTargets);
  for (const unit of units) {
    const sub = getIn(raw, unit);
    config = setInImmutable(
      config,
      unit,
      sub === MISSING ? MISSING : resolver.resolveSubtree(sub, unit),
    );
  }
  for (const refPointer of affected) {
    const refPath = pointerToPath(refPointer);
    if (units.some((u) => isPrefixPath(u, refPath))) continue;
    const node = getIn(raw, refPath);
    if (node === MISSING) continue;
    config = setInImmutable(config, refPath, resolver.resolveSubtree(node, refPath));
    recomputed.add(refPointer);
  }

  applyRefExplanations(explain, dependencies, resolvedRefs, affected);
  return {
    state: { layers, raw, config, explain, dependencies, resolvedRefs, resolvedTargets },
    recomputed: [...recomputed],
  };
}
