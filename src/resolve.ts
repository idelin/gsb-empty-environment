import {
  formatPath,
  isPlainObject,
  type JsonObject,
  type JsonValue,
  type Path,
} from "./json.ts";
import { pickResolution, type Conflict, type Resolution } from "./merge.ts";

export interface ResolveOptions {
  readonly idKey?: string;
}

export function resolveConflict(
  merged: JsonValue,
  conflict: Conflict,
  resolution: Resolution,
  options: ResolveOptions = {},
): JsonValue {
  const idKey = options.idKey ?? "id";
  if (conflict.reason === "reorder") {
    return applyReorder(merged, conflict, resolution, idKey);
  }
  const value = pickResolution(conflict, resolution);
  return setAtPath(merged, conflict.path, value, conflict.itemId, idKey) ?? null;
}

export function resolveAll(
  merged: JsonValue,
  conflicts: readonly Conflict[],
  resolutions:
    | readonly Resolution[]
    | ((conflict: Conflict, index: number) => Resolution),
  options: ResolveOptions = {},
): JsonValue {
  const pick =
    typeof resolutions === "function"
      ? resolutions
      : (_: Conflict, i: number) => resolutions[i];
  let doc = merged;
  conflicts.forEach((conflict, i) => {
    doc = resolveConflict(doc, conflict, pick(conflict, i), options);
  });
  return doc;
}

function getAtPath(node: JsonValue, path: Path): JsonValue | undefined {
  let cur: JsonValue | undefined = node;
  for (const seg of path) {
    if (cur === undefined || cur === null || typeof cur !== "object") return undefined;
    cur = (cur as JsonObject)[seg as string];
  }
  return cur;
}

function setAtPath(
  node: JsonValue | undefined,
  path: Path,
  value: JsonValue | undefined,
  itemId: string | number | undefined,
  idKey: string,
): JsonValue | undefined {
  if (path.length === 0) return value;
  const [head, ...rest] = path;

  if (typeof head === "number") {
    if (!Array.isArray(node)) {
      throw new Error(`cannot resolve conflict: expected an array at ${formatPath(path)}`);
    }
    const arr = [...node];
    let index = head;
    if (rest.length === 0 && itemId !== undefined) {
      const found = arr.findIndex(
        (it) => isPlainObject(it) && it[idKey] === itemId,
      );
      if (found !== -1) index = found;
      else if (value === undefined) return arr;
      else {
        arr.splice(Math.min(head, arr.length), 0, value);
        return arr;
      }
    }
    if (rest.length === 0) {
      if (value === undefined) {
        if (index < arr.length) arr.splice(index, 1);
      } else if (index < arr.length) {
        arr[index] = value;
      } else {
        arr.push(value);
      }
    } else {
      const child = setAtPath(arr[index], rest, value, itemId, idKey);
      if (child === undefined) arr.splice(index, 1);
      else arr[index] = child;
    }
    return arr;
  }

  if (!isPlainObject(node)) {
    throw new Error(`cannot resolve conflict: expected an object at ${formatPath(path)}`);
  }
  const out: JsonObject = { ...node };
  const child = setAtPath(out[head], rest, value, itemId, idKey);
  if (child === undefined) delete out[head];
  else out[head] = child;
  return out;
}

function applyReorder(
  merged: JsonValue,
  conflict: Conflict,
  resolution: Resolution,
  idKey: string,
): JsonValue {
  const seq = pickResolution(conflict, resolution);
  if (!Array.isArray(seq)) {
    throw new Error("reorder conflicts must be resolved with an array of item ids");
  }
  const target = getAtPath(merged, conflict.path);
  if (!Array.isArray(target)) {
    throw new Error(`cannot resolve conflict: expected an array at ${formatPath(conflict.path)}`);
  }
  const rank = new Map(seq.map((id, i) => [id, i]));
  const matched = target.filter(
    (it) => isPlainObject(it) && rank.has(it[idKey] as string | number),
  );
  matched.sort(
    (a, b) =>
      rank.get((a as JsonObject)[idKey] as string | number)! -
      rank.get((b as JsonObject)[idKey] as string | number)!,
  );
  const out = [...target];
  let i = 0;
  for (let k = 0; k < out.length; k++) {
    const item = out[k];
    if (isPlainObject(item) && rank.has(item[idKey] as string | number)) {
      out[k] = matched[i++];
    }
  }
  return setAtPath(merged, conflict.path, out, undefined, idKey) ?? null;
}
