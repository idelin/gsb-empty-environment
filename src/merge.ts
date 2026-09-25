import {
  deepEqual,
  isPlainObject,
  type JsonObject,
  type JsonValue,
  type Path,
} from "./json.ts";
import { MergeError, type MergeSide } from "./errors.ts";

export type ConflictReason =
  | "modify-modify"
  | "delete-modify"
  | "add-add"
  | "reorder";

export interface Conflict {
  readonly path: Path;
  readonly reason: ConflictReason;
  readonly base: JsonValue | undefined;
  readonly ours: JsonValue | undefined;
  readonly theirs: JsonValue | undefined;
  readonly itemId?: string | number;
}

export type Resolution =
  | { readonly take: "ours" | "theirs" | "base" }
  | { readonly value: JsonValue };

export interface MergeOptions {
  readonly idKey?: string;
  readonly onConflict?: (conflict: Conflict) => Resolution;
}

export interface MergeResult {
  readonly merged: JsonValue;
  readonly conflicts: readonly Conflict[];
}

type Id = string | number;

interface Ctx {
  readonly idKey: string;
  readonly conflicts: Conflict[];
  readonly onConflict?: (conflict: Conflict) => Resolution;
}

export function pickResolution(
  conflict: Conflict,
  resolution: Resolution,
): JsonValue | undefined {
  if ("value" in resolution) return resolution.value;
  return conflict[resolution.take];
}

export function merge3(
  base: JsonValue,
  ours: JsonValue,
  theirs: JsonValue,
  options: MergeOptions = {},
): MergeResult {
  const ctx: Ctx = {
    idKey: options.idKey ?? "id",
    conflicts: [],
    onConflict: options.onConflict,
  };
  validateInput(ctx.idKey, base, "base", []);
  validateInput(ctx.idKey, ours, "ours", []);
  validateInput(ctx.idKey, theirs, "theirs", []);
  const merged = mergeNode(ctx, base, ours, theirs, [], undefined) ?? null;
  return { merged, conflicts: ctx.conflicts };
}

function validateInput(idKey: string, node: JsonValue, side: MergeSide, path: Path): void {
  if (Array.isArray(node)) {
    indexById(idKey, node, path, side);
    node.forEach((item, i) => validateInput(idKey, item, side, [...path, i]));
    return;
  }
  if (isPlainObject(node)) {
    for (const [key, value] of Object.entries(node)) {
      validateInput(idKey, value, side, [...path, key]);
    }
  }
}

function recordConflict(
  ctx: Ctx,
  path: Path,
  reason: ConflictReason,
  base: JsonValue | undefined,
  ours: JsonValue | undefined,
  theirs: JsonValue | undefined,
  itemId: Id | undefined,
): JsonValue | undefined {
  const conflict: Conflict = {
    path,
    reason,
    base,
    ours,
    theirs,
    ...(itemId === undefined ? {} : { itemId }),
  };
  ctx.conflicts.push(conflict);
  if (ctx.onConflict) return pickResolution(conflict, ctx.onConflict(conflict));
  return ours;
}

function mergeNode(
  ctx: Ctx,
  base: JsonValue | undefined,
  ours: JsonValue | undefined,
  theirs: JsonValue | undefined,
  path: Path,
  itemId: Id | undefined,
): JsonValue | undefined {
  if (deepEqual(ours, theirs)) return ours;
  if (deepEqual(base, ours)) return theirs;
  if (deepEqual(base, theirs)) return ours;

  if (ours === undefined || theirs === undefined) {
    return recordConflict(ctx, path, "delete-modify", base, ours, theirs, itemId);
  }
  if (isPlainObject(ours) && isPlainObject(theirs)) {
    return mergeObject(ctx, isPlainObject(base) ? base : {}, ours, theirs, path, itemId);
  }
  if (Array.isArray(ours) && Array.isArray(theirs)) {
    return mergeArray(ctx, Array.isArray(base) ? base : [], ours, theirs, path);
  }
  const reason: ConflictReason = base === undefined ? "add-add" : "modify-modify";
  return recordConflict(ctx, path, reason, base, ours, theirs, itemId);
}

function mergeObject(
  ctx: Ctx,
  base: JsonObject,
  ours: JsonObject,
  theirs: JsonObject,
  path: Path,
  itemId: Id | undefined,
): JsonObject {
  const out: JsonObject = {};
  const keys = new Set([...Object.keys(ours), ...Object.keys(theirs)]);
  for (const key of keys) {
    const value = mergeNode(ctx, base[key], ours[key], theirs[key], [...path, key], itemId);
    if (value !== undefined) out[key] = value;
  }
  return out;
}

interface Entry {
  readonly value: JsonValue;
  readonly index: number;
}

function indexById(
  idKey: string,
  arr: JsonValue[],
  path: Path,
  side: MergeSide,
): Map<Id, Entry> {
  const map = new Map<Id, Entry>();
  arr.forEach((item, index) => {
    const itemPath = [...path, index];
    if (!isPlainObject(item)) {
      throw new MergeError(
        "MISSING_ID",
        side,
        itemPath,
        `array items must be objects carrying a stable "${idKey}" key`,
      );
    }
    const id: JsonValue | undefined = item[idKey];
    if (id === undefined) {
      throw new MergeError(
        "MISSING_ID",
        side,
        itemPath,
        `array item is missing the "${idKey}" key`,
      );
    }
    if (typeof id !== "string" && typeof id !== "number") {
      throw new MergeError(
        "INVALID_ID",
        side,
        itemPath,
        `"${idKey}" must be a string or number`,
      );
    }
    if (map.has(id)) {
      throw new MergeError(
        "DUPLICATE_ID",
        side,
        itemPath,
        `duplicate "${idKey}" value ${JSON.stringify(id)}`,
      );
    }
    map.set(id, { value: item, index });
  });
  return map;
}

function mergeArray(
  ctx: Ctx,
  base: JsonValue[],
  ours: JsonValue[],
  theirs: JsonValue[],
  path: Path,
): JsonValue[] {
  const baseMap = indexById(ctx.idKey, base, path, "base");
  const oursMap = indexById(ctx.idKey, ours, path, "ours");
  const theirsMap = indexById(ctx.idKey, theirs, path, "theirs");

  const ids = new Set<Id>([...baseMap.keys(), ...oursMap.keys(), ...theirsMap.keys()]);
  const mergedById = new Map<Id, JsonValue>();
  for (const id of ids) {
    const b = baseMap.get(id);
    const o = oursMap.get(id);
    const t = theirsMap.get(id);
    const index = (o ?? t ?? b)!.index;
    const value = mergeNode(ctx, b?.value, o?.value, t?.value, [...path, index], id);
    if (value !== undefined) mergedById.set(id, value);
  }

  const order = mergeOrder(ctx, baseMap, oursMap, theirsMap, mergedById, path);
  return order.map((id) => mergedById.get(id)!);
}

function survivingIds(map: Map<Id, Entry>, mergedById: Map<Id, JsonValue>): Id[] {
  return [...map.keys()].filter((id) => mergedById.has(id));
}

function sameSequence(a: readonly Id[], b: readonly Id[]): boolean {
  return a.length === b.length && a.every((id, i) => id === b[i]);
}

function mergeOrder(
  ctx: Ctx,
  baseMap: Map<Id, Entry>,
  oursMap: Map<Id, Entry>,
  theirsMap: Map<Id, Entry>,
  mergedById: Map<Id, JsonValue>,
  path: Path,
): Id[] {
  const baseSeq = survivingIds(baseMap, mergedById);
  const oursSeq = survivingIds(oursMap, mergedById);
  const theirsSeq = survivingIds(theirsMap, mergedById);

  const baseIds = new Set(baseSeq);
  const oursSpine = oursSeq.filter((id) => baseIds.has(id));
  const theirsSpine = theirsSeq.filter((id) => baseIds.has(id));

  let spine: Id[];
  if (sameSequence(oursSpine, theirsSpine)) {
    spine = oursSpine;
  } else if (sameSequence(baseSeq, oursSpine)) {
    spine = theirsSpine;
  } else if (sameSequence(baseSeq, theirsSpine)) {
    spine = oursSpine;
  } else {
    const conflict: Conflict = {
      path,
      reason: "reorder",
      base: baseSeq,
      ours: oursSpine,
      theirs: theirsSpine,
    };
    ctx.conflicts.push(conflict);
    spine = oursSpine;
    if (ctx.onConflict) {
      const chosen = pickResolution(conflict, ctx.onConflict(conflict));
      if (Array.isArray(chosen)) spine = chosen as Id[];
    }
  }

  const order = [...spine];
  for (const seq of [oursSeq, theirsSeq]) {
    let anchor = -1;
    for (const id of seq) {
      const at = order.indexOf(id);
      if (at !== -1) {
        anchor = at;
        continue;
      }
      order.splice(anchor + 1, 0, id);
      anchor += 1;
    }
  }
  return order;
}
