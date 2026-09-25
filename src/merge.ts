import {
  cloneJson,
  encodePath,
  isArray,
  isObject,
  jsonEquals,
  type JsonObject,
  type JsonValue,
  type PathSeg,
} from "./json.ts";
import { isDeleteSentinel, isRef } from "./markers.ts";
import { ConfigError } from "./errors.ts";
import type { StrategyMap } from "./strategy.ts";

/** Marker for "no value" (distinct from null, which is a real JSON value). */
export const ABSENT: unique symbol = Symbol("absent");
export type MaybeAbsent = JsonValue | typeof ABSENT;

/** Structured provenance: entries are typed objects, never formatted strings. */
export type ProvenanceEntry =
  | {
      kind: "layer";
      layer: string;
      priority: number;
      op: "set" | "merge" | "override" | "delete" | "byId";
      path: PathSeg[];
      id?: string | number;
      value?: JsonValue;
    }
  | { kind: "ref"; target: PathSeg[]; resolvedFrom: PathSeg[] };

export interface LayerContrib {
  id: string;
  priority: number;
}

/** Per-field source chains, keyed by encoded path. */
export class Explain {
  readonly map = new Map<string, ProvenanceEntry[]>();

  add(path: readonly PathSeg[], entry: ProvenanceEntry): void {
    const key = encodePath(path);
    const list = this.map.get(key);
    if (list) list.push(entry);
    else this.map.set(key, [entry]);
  }

  /** Remove entries strictly below `path` (a set/delete at `path` invalidates deeper chains). */
  pruneUnder(path: readonly PathSeg[]): void {
    const prefix = encodePath(path);
    for (const key of this.map.keys()) {
      if (prefix === "" ? key !== "" : key.startsWith(prefix + "/")) this.map.delete(key);
    }
  }

  /** Remove entries at or below `path`. */
  removeAtOrUnder(path: readonly PathSeg[]): void {
    const prefix = encodePath(path);
    for (const key of this.map.keys()) {
      if (key === prefix || (prefix === "" ? key !== "" : key.startsWith(prefix + "/"))) this.map.delete(key);
    }
  }

  removeRefEntriesAt(path: readonly PathSeg[]): void {
    const key = encodePath(path);
    const list = this.map.get(key);
    if (!list) return;
    const kept = list.filter((e) => e.kind !== "ref");
    if (kept.length === 0) this.map.delete(key);
    else this.map.set(key, kept);
  }

  mergeFrom(other: Explain): void {
    for (const [k, v] of other.map) this.map.set(k, v);
  }

  clone(): Explain {
    const out = new Explain();
    for (const [k, v] of this.map) out.map.set(k, [...v]);
    return out;
  }
}

export interface MergeCtx {
  strategies: StrategyMap;
  explain: Explain;
}

function record(
  ctx: MergeCtx,
  path: readonly PathSeg[],
  layer: LayerContrib,
  op: "set" | "merge" | "override" | "delete" | "byId",
  extra: { id?: string | number; value?: JsonValue } = {},
): void {
  ctx.explain.add(path, {
    kind: "layer",
    layer: layer.id,
    priority: layer.priority,
    op,
    path: [...path],
    ...(extra.id !== undefined ? { id: extra.id } : {}),
    ...(extra.value !== undefined ? { value: cloneJson(extra.value) } : {}),
  });
}

/** Two same-id items conflict when both declare a `type` field with different
 *  values. The whole commit batch must be rejected when this happens. */
function typeConflict(existing: JsonValue, incoming: JsonValue): { a: JsonValue; b: JsonValue } | undefined {
  if (isObject(existing) && isObject(incoming)) {
    const ta = existing["type"];
    const tb = incoming["type"];
    if (ta !== undefined && tb !== undefined && !jsonEquals(ta, tb)) return { a: ta, b: tb };
  }
  return undefined;
}

/** Deep merge of two byId items (incoming wins on scalars/arrays/kind changes). */
function mergeItem(lower: JsonValue, upper: JsonValue): JsonValue {
  if (isObject(lower) && isObject(upper)) {
    const out: JsonObject = { ...lower };
    for (const k of Object.keys(upper)) {
      out[k] = k in lower ? mergeItem(lower[k], upper[k]) : cloneJson(upper[k]);
    }
    return out;
  }
  return cloneJson(upper);
}

function itemId(item: JsonValue): string | number | undefined {
  if (!isObject(item)) return undefined;
  const id = item["id"];
  return typeof id === "string" || typeof id === "number" ? id : undefined;
}

/** Set-union merge of arrays of `{id, ...}` objects, ordered by first appearance. */
export function mergeById(
  ctx: MergeCtx,
  path: readonly PathSeg[],
  lower: MaybeAbsent,
  upper: JsonValue,
  layer: LayerContrib,
): MaybeAbsent {
  if (!isArray(upper)) {
    throw new ConfigError("INVALID_LAYER", `byId field must be an array`, {
      path: [...path],
      layer: layer.id,
    });
  }
  const list: JsonValue[] = lower === ABSENT ? [] : (lower as JsonValue[]).slice();
  for (let i = 0; i < upper.length; i++) {
    const item = upper[i];
    const itemPath = [...path, i];
    if (isDeleteSentinel(item)) {
      const id = itemId(item);
      if (id === undefined) {
        throw new ConfigError("MISSING_ID", `delete-sentinel item needs an "id"`, {
          path: itemPath,
          layer: layer.id,
        });
      }
      const idx = list.findIndex((x) => itemId(x) === id);
      if (idx >= 0) list.splice(idx, 1);
      record(ctx, path, layer, "delete", { id });
      continue;
    }
    const id = itemId(item);
    if (id === undefined) {
      throw new ConfigError("MISSING_ID", `byId item must be an object with a string/number "id"`, {
        path: itemPath,
        layer: layer.id,
      });
    }
    const idx = list.findIndex((x) => itemId(x) === id);
    if (idx < 0) {
      list.push(cloneJson(item));
      record(ctx, path, layer, "byId", { id, value: item });
      continue;
    }
    const conflict = typeConflict(list[idx], item);
    if (conflict) {
      throw new ConfigError("TYPE_CONFLICT", `byId items with id ${JSON.stringify(id)} declare conflicting types`, {
        path,
        layer: layer.id,
        details: { id, existingType: conflict.a, incomingType: conflict.b },
      });
    }
    list[idx] = mergeItem(list[idx], item);
    record(ctx, path, layer, "byId", { id, value: item });
  }
  return list;
}

/** Fold one layer's value at `path` into the accumulated value.
 *  Path-local: the result depends only on layer values at this exact path. */
export function mergeValue(
  ctx: MergeCtx,
  path: readonly PathSeg[],
  lower: MaybeAbsent,
  upper: JsonValue,
  layer: LayerContrib,
): MaybeAbsent {
  // The root node itself carries no provenance: per-field chains start at
  // top-level fields, which keeps full and incremental compiles identical.
  const rec = (
    op: "set" | "merge" | "override" | "delete" | "byId",
    extra: { id?: string | number; value?: JsonValue } = {},
  ): void => {
    if (path.length > 0) record(ctx, path, layer, op, extra);
  };
  if (isDeleteSentinel(upper)) {
    rec("delete");
    ctx.explain.pruneUnder(path);
    return ABSENT;
  }
  // Refs are atomic pure markers: never deep-merge into or out of them.
  if (isRef(upper) || (lower !== ABSENT && isRef(lower))) {
    rec("set", { value: upper });
    ctx.explain.pruneUnder(path);
    return cloneJson(upper);
  }
  const strategy = ctx.strategies.strategyAt(path);
  if (strategy === "byId") {
    return mergeById(ctx, path, lower, upper, layer);
  }
  if (strategy !== "override" && isObject(upper) && (lower === ABSENT || isObject(lower))) {
    rec("merge");
    const base: JsonObject = lower === ABSENT ? {} : (lower as JsonObject);
    const out: JsonObject = { ...base };
    for (const k of Object.keys(upper)) {
      const child = mergeValue(
        ctx,
        [...path, k],
        Object.prototype.hasOwnProperty.call(base, k) ? base[k] : ABSENT,
        upper[k],
        layer,
      );
      if (child === ABSENT) delete out[k];
      else out[k] = child;
    }
    return out;
  }
  // Leaf set or explicit override: higher layer wins wholesale.
  rec(strategy === "override" ? "override" : "set", { value: upper });
  ctx.explain.pruneUnder(path);
  return cloneJson(upper);
}
