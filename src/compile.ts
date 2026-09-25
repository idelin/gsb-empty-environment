import {
  cloneJson,
  encodePath,
  getPath,
  minimalCover,
  pathsComparable,
  setPathImmutable,
  deletePathImmutable,
  type JsonValue,
  type PathSeg,
} from "./json.ts";
import { ABSENT, Explain, mergeValue, type MaybeAbsent, type ProvenanceEntry } from "./merge.ts";
import { collectRefs, resolveRefs, RefGraph } from "./refs.ts";
import type { StrategyMap } from "./strategy.ts";

/** Declarative condition on the evaluation context, or a predicate function. */
export type Condition =
  | { path: string | PathSeg[]; equals: JsonValue }
  | ((context: JsonValue) => boolean);

export interface LayerState {
  id: string;
  priority: number;
  data: JsonValue;
  /** manual toggle, default true */
  enabled: boolean;
  /** conditional enablement, evaluated against the store context */
  when?: Condition;
  /** insertion order, stabilizes priority ties */
  order: number;
}

export function evalCondition(when: Condition, context: JsonValue): boolean {
  if (typeof when === "function") return !!when(context);
  const segs: PathSeg[] = typeof when.path === "string"
    ? when.path.split(".").filter((s) => s.length > 0)
    : when.path;
  const value = getPath(context, segs);
  return value === when.equals;
}

export function isLayerEnabled(layer: LayerState, context: JsonValue): boolean {
  return layer.enabled && (layer.when === undefined || evalCondition(layer.when, context));
}

export interface CompileStats {
  full: boolean;
  /** encoded paths that were actually re-merged in this compile */
  recomputedPaths: string[];
  /** encoded ref locations re-resolved in this compile */
  resolvedRefs: string[];
}

export interface CompileResult {
  /** merged tree with refs still unresolved (internal, for incremental compiles) */
  raw: JsonValue;
  /** fully resolved config */
  config: JsonValue;
  explain: Explain;
  graph: RefGraph;
  /** encoded ref location -> resolved value (cache for the next incremental compile) */
  resolved: Map<string, JsonValue>;
  stats: CompileStats;
}

function enabledInOrder(layers: readonly LayerState[], context: JsonValue): LayerState[] {
  return layers
    .filter((l) => isLayerEnabled(l, context))
    .sort((a, b) => a.priority - b.priority || a.order - b.order);
}

/** From-scratch compile: fold every enabled layer, then resolve all refs. */
export function compileFull(
  layers: readonly LayerState[],
  context: JsonValue,
  strategies: StrategyMap,
): CompileResult {
  const explain = new Explain();
  const ctx = { strategies, explain };
  let acc: MaybeAbsent = ABSENT;
  for (const layer of enabledInOrder(layers, context)) {
    acc = mergeValue(ctx, [], acc, layer.data, layer);
  }
  const raw: JsonValue = acc === ABSENT ? {} : acc;
  const graph = new RefGraph();
  collectRefs(raw, [], graph);
  const { config, resolved } = resolveRefs(raw, graph, explain);
  return {
    raw,
    config,
    explain,
    graph,
    resolved,
    stats: { full: true, recomputedPaths: [""], resolvedRefs: [...graph.locations.keys()] },
  };
}

/** Refs whose resolution may change when `dirty` paths change (transitive closure). */
export function computeAffectedRefs(graph: RefGraph, dirty: readonly PathSeg[][]): Set<string> {
  const affectedPaths: PathSeg[][] = dirty.map((p) => [...p]);
  const affected = new Set<string>();
  let changed = true;
  while (changed) {
    changed = false;
    for (const [key, loc] of graph.locations) {
      if (affected.has(key)) continue;
      const target = graph.targets.get(key) as PathSeg[];
      const hit = affectedPaths.some((a) => pathsComparable(a, loc) || pathsComparable(a, target));
      if (hit) {
        affected.add(key);
        affectedPaths.push([...loc]);
        changed = true;
      }
    }
  }
  return affected;
}

/** Incremental compile: re-merge only the dirty subtrees and re-resolve only the
 *  refs whose location or target touches them. Produces a result that must be
 *  identical to `compileFull` on the same inputs (verified by tests). */
export function compileIncremental(
  prev: CompileResult,
  layers: readonly LayerState[],
  context: JsonValue,
  strategies: StrategyMap,
  dirty: readonly PathSeg[][],
): CompileResult {
  const roots = minimalCover(dirty.map((p) => [...p]));
  if (roots.some((p) => p.length === 0)) return compileFull(layers, context, strategies);

  const ordered = enabledInOrder(layers, context);
  const explain = prev.explain.clone();
  const ctx = { strategies, explain };
  const graph = prev.graph.clone();
  let raw = prev.raw;

  for (const root of roots) {
    explain.removeAtOrUnder(root);
    graph.removeAtOrUnder(root);
    let acc: MaybeAbsent = ABSENT;
    for (const layer of ordered) {
      const v = getPath(layer.data, root);
      if (v !== undefined) acc = mergeValue(ctx, root, acc, v, layer);
    }
    if (acc === ABSENT) raw = deletePathImmutable(raw, root);
    else {
      raw = setPathImmutable(raw, root, acc);
      collectRefs(acc, [...root], graph);
    }
  }

  const affected = computeAffectedRefs(graph, roots);
  const { config, resolved } = resolveRefs(raw, graph, explain, {
    cache: prev.resolved,
    only: affected,
  });

  return {
    raw,
    config,
    explain,
    graph,
    resolved,
    stats: {
      full: false,
      recomputedPaths: roots.map((p) => encodePath(p)),
      resolvedRefs: [...affected],
    },
  };
}

export type { Explain, ProvenanceEntry };
export { cloneJson };
