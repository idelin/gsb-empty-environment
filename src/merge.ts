import { deepClone, isPlainObject } from "./json.ts";
import type { JsonObject, JsonValue, Path } from "./json.ts";
import { isDeleteSentinel } from "./sentinel.ts";
import { isRefNode } from "./refs.ts";
import { FieldValidationError, MergeTypeConflictError } from "./errors.ts";
import type { StrategyMap } from "./strategy.ts";

/**
 * One entry in a field's provenance chain. Contributions are structured
 * records (layer id + field path + cloned value snapshot), never formatted
 * strings, so provenance can be traversed programmatically.
 */
export interface Contribution {
  readonly layerId: string;
  readonly path: Path;
  readonly kind: "value" | "delete" | "merge" | "delete-item";
  readonly value: JsonValue | undefined;
}

/** Full per-field explanation: strategy, outcome and the provenance chain. */
export interface ExplainRecord {
  readonly pointer: string;
  readonly path: Path;
  readonly strategy: "override" | "deep" | "byId";
  readonly present: boolean;
  readonly value: JsonValue | undefined;
  readonly winner: string | undefined;
  readonly contributions: readonly Contribution[];
  /** For reference fields: pointer of the field this one resolved from. */
  readonly resolvedFrom: string | undefined;
}

/** One layer's value at a field path, lowest priority first. */
export interface LayerInput {
  readonly layerId: string;
  readonly present: boolean;
  readonly value: JsonValue | undefined;
}

export interface MergeOutcome {
  readonly present: boolean;
  readonly value: JsonValue | undefined;
}

export type RecordSink = (
  path: Path,
  contributions: Contribution[],
  winner: string | undefined,
  present: boolean,
  value: JsonValue | undefined,
) => void;

/** Objects that participate in deep key-wise merging (refs and the delete
 *  sentinel are leaves, not mergeable objects). */
function isMergeableObject(v: unknown): v is JsonObject {
  return isPlainObject(v) && !isDeleteSentinel(v) && !isRefNode(v);
}

/** Merges all layers' values at one field path according to its strategy. */
export function mergeField(
  entries: readonly LayerInput[],
  path: Path,
  strategies: StrategyMap,
  sink: RecordSink,
): MergeOutcome {
  const strat = strategies.strategyAt(path);
  if (strat.kind === "byId") {
    return mergeByIdField(entries, path, strat.idKey, sink);
  }
  return mergeScalarOrDeep(entries, path, strategies, sink, strat.kind === "deep");
}

function mergeScalarOrDeep(
  entries: readonly LayerInput[],
  path: Path,
  strategies: StrategyMap,
  sink: RecordSink,
  deep: boolean,
): MergeOutcome {
  const present = entries.filter((e) => e.present);
  if (present.length === 0) return { present: false, value: undefined };

  const top = present[present.length - 1];
  if (isDeleteSentinel(top.value)) {
    // Highest-priority layer deletes the field: masks everything below.
    sink(path, contributionsOf(present, path), undefined, false, undefined);
    return { present: false, value: undefined };
  }

  const nonDelete = present.filter((e) => !isDeleteSentinel(e.value));
  const allObjects =
    nonDelete.length === present.length &&
    nonDelete.every((e) => isMergeableObject(e.value));

  if (deep && allObjects) {
    const keys: string[] = [];
    const seen = new Set<string>();
    for (const e of nonDelete) {
      for (const k of Object.keys(e.value as JsonObject)) {
        if (!seen.has(k)) {
          seen.add(k);
          keys.push(k);
        }
      }
    }
    const out: JsonObject = {};
    for (const k of keys) {
      const childEntries: LayerInput[] = present.map((e) => {
        const v = e.value as JsonObject;
        const has = Object.hasOwn(v, k);
        return { layerId: e.layerId, present: has, value: has ? v[k] : undefined };
      });
      const child = mergeField(childEntries, [...path, k], strategies, sink);
      if (child.present) out[k] = child.value as JsonValue;
    }
    return { present: true, value: out };
  }

  // Leaf decision (or `override`): highest-priority non-delete value wins.
  const winner = nonDelete[nonDelete.length - 1];
  const value = deepClone(winner.value) as JsonValue;
  sink(path, contributionsOf(present, path), winner.layerId, true, value);
  return { present: true, value };
}

function contributionsOf(present: readonly LayerInput[], path: Path): Contribution[] {
  return present.map((e) => ({
    layerId: e.layerId,
    path,
    kind: isDeleteSentinel(e.value) ? ("delete" as const) : ("value" as const),
    value: isDeleteSentinel(e.value) ? undefined : deepClone(e.value),
  }));
}

function mergeByIdField(
  entries: readonly LayerInput[],
  path: Path,
  idKey: string,
  sink: RecordSink,
): MergeOutcome {
  const present = entries.filter((e) => e.present);
  if (present.length === 0) return { present: false, value: undefined };

  const contributions: Contribution[] = [];
  const items = new Map<string, JsonValue>();
  const order: string[] = [];
  let sawArray = false;
  let winner: string | undefined;

  for (const e of present) {
    const v = e.value;
    if (isDeleteSentinel(v)) {
      contributions.push({ layerId: e.layerId, path, kind: "delete", value: undefined });
      items.clear();
      order.length = 0;
      sawArray = false;
      winner = undefined;
      continue;
    }
    if (!Array.isArray(v)) {
      throw new FieldValidationError(`byId field must be an array`, path, e.layerId);
    }
    sawArray = true;
    winner = e.layerId;
    v.forEach((item, idx) => {
      const itemPath = [...path, String(idx)];
      if (isDeleteSentinel(item)) {
        throw new FieldValidationError(
          `delete item must carry the id key "${idKey}"`,
          itemPath,
          e.layerId,
        );
      }
      if (isPlainObject(item) && item.$delete === true) {
        const id = itemId(item, idKey, itemPath, e.layerId);
        contributions.push({
          layerId: e.layerId,
          path: itemPath,
          kind: "delete-item",
          value: deepClone(item),
        });
        if (items.delete(id)) {
          const at = order.indexOf(id);
          if (at >= 0) order.splice(at, 1);
        }
        return;
      }
      const id = itemId(item, idKey, itemPath, e.layerId);
      const existing = items.get(id);
      if (existing === undefined) {
        items.set(id, deepClone(item));
        order.push(id);
        contributions.push({
          layerId: e.layerId,
          path: itemPath,
          kind: "value",
          value: deepClone(item),
        });
        return;
      }
      const existingIsObject = isPlainObject(existing);
      const incomingIsObject = isPlainObject(item);
      if (existingIsObject !== incomingIsObject) {
        // Same id, conflicting types: reject the whole batch.
        throw new MergeTypeConflictError(
          `conflicting item types for id ${JSON.stringify(id)}`,
          itemPath,
          e.layerId,
        );
      }
      if (existingIsObject) {
        items.set(id, deepMergeObjects(existing as JsonObject, item as JsonObject));
        contributions.push({
          layerId: e.layerId,
          path: itemPath,
          kind: "merge",
          value: deepClone(item),
        });
      }
      // Identical primitives contribute nothing new.
    });
  }

  const value: JsonValue = order.map((id) => items.get(id) as JsonValue);
  sink(path, contributions, winner, sawArray, sawArray ? value : undefined);
  return { present: sawArray, value: sawArray ? value : undefined };
}

function itemId(item: JsonValue, idKey: string, itemPath: Path, layerId: string): string {
  if (isPlainObject(item)) {
    const id = item[idKey];
    if (typeof id !== "string" && typeof id !== "number") {
      throw new FieldValidationError(
        `byId item is missing id key "${idKey}"`,
        itemPath,
        layerId,
      );
    }
    return String(id);
  }
  if (typeof item === "string" || typeof item === "number") return String(item);
  throw new FieldValidationError(
    `byId item must be an object with key "${idKey}" or a string/number`,
    itemPath,
    layerId,
  );
}

/** Deep merge of two same-id object items; the higher layer wins conflicts. */
function deepMergeObjects(lower: JsonObject, higher: JsonObject): JsonObject {
  const out: JsonObject = { ...lower };
  for (const [k, v] of Object.entries(higher)) {
    const cur = out[k];
    out[k] = isPlainObject(cur) && isPlainObject(v) ? deepMergeObjects(cur, v) : deepClone(v);
  }
  return out;
}
