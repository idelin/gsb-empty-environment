export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonObject = { [key: string]: JsonValue };

export type Side = "base" | "ours" | "theirs";

export type PathSegment = string | { id: string | number } | { index: number };
export type Path = PathSegment[];

export const ABSENT = Symbol("json3.absent");
export type Absent = typeof ABSENT;

export const DELETE = Symbol("json3.delete");
export type Delete = typeof DELETE;

export interface Conflict {
  path: Path;
  base: JsonValue | Absent;
  ours: JsonValue | Absent;
  theirs: JsonValue | Absent;
}

export interface Resolution {
  path: Path | string;
  value: JsonValue | Delete;
}

export interface MergeOptions {
  idKey?: string;
  resolutions?: Resolution[];
}

export interface MergeResult {
  tree: JsonValue | undefined;
  conflicts: Conflict[];
  ok: boolean;
  unusedResolutions: Resolution[];
}

export class MergeInputError extends Error {
  readonly path: Path;
  readonly source: Side;

  constructor(message: string, path: Path, source: Side) {
    super(`${message} (source: ${source}, path: ${formatPath(path)})`);
    this.name = "MergeInputError";
    this.path = path;
    this.source = source;
  }
}

export function formatPath(path: Path): string {
  let out = "$";
  for (const segment of path) {
    if (typeof segment === "string") {
      out += /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(segment)
        ? `.${segment}`
        : `[${JSON.stringify(segment)}]`;
    } else if ("id" in segment) {
      out += `[id=${JSON.stringify(segment.id)}]`;
    } else {
      out += `[${segment.index}]`;
    }
  }
  return out;
}

export function jsonEqual(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true;
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) {
    return false;
  }
  const aIsArray = Array.isArray(a);
  const bIsArray = Array.isArray(b);
  if (aIsArray !== bIsArray) return false;
  if (aIsArray && bIsArray) {
    return a.length === b.length && a.every((item, i) => jsonEqual(item, b[i]));
  }
  const aObj = a as JsonObject;
  const bObj = b as JsonObject;
  const aKeys = Object.keys(aObj);
  const bKeys = Object.keys(bObj);
  if (aKeys.length !== bKeys.length) return false;
  return aKeys.every(
    (key) =>
      Object.prototype.hasOwnProperty.call(bObj, key) &&
      jsonEqual(aObj[key], bObj[key]),
  );
}

function isPlainObject(value: unknown): value is JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return false;
  }
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

function hasOwn(obj: JsonObject, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(obj, key);
}

function fail(message: string, path: Path, source: Side): never {
  throw new MergeInputError(message, path, source);
}

function validateValue(value: unknown, path: Path, source: Side, idKey: string): void {
  if (value === null || typeof value === "boolean" || typeof value === "string") {
    return;
  }
  if (typeof value === "number") {
    if (!Number.isFinite(value)) {
      fail("non-finite number is not valid JSON", path, source);
    }
    return;
  }
  if (Array.isArray(value)) {
    const seen = new Set<string>();
    value.forEach((item, index) => {
      const indexPath = [...path, { index }];
      if (!isPlainObject(item)) {
        fail(
          `array items must be plain objects carrying a stable "${idKey}" id`,
          indexPath,
          source,
        );
      }
      const id = (item as JsonObject)[idKey];
      if (
        (typeof id !== "string" && typeof id !== "number") ||
        (typeof id === "number" && !Number.isFinite(id))
      ) {
        fail(`array item is missing a valid "${idKey}" id`, indexPath, source);
      }
      const key = idKeyOf(id);
      if (seen.has(key)) {
        fail(`duplicate id ${JSON.stringify(id)} in array`, [...path, { id }], source);
      }
      seen.add(key);
      validateValue(item, [...path, { id }], source, idKey);
    });
    return;
  }
  if (isPlainObject(value)) {
    for (const key of Object.keys(value)) {
      validateValue(value[key], [...path, key], source, idKey);
    }
    return;
  }
  fail(`value of type ${typeof value} is not valid JSON`, path, source);
}

function idKeyOf(id: string | number): string {
  return `${typeof id}:${String(id)}`;
}

type MaybeAbsent = JsonValue | Absent;

interface Ctx {
  idKey: string;
  conflicts: Conflict[];
  resolutions: Map<string, JsonValue | Delete>;
  usedResolutions: Set<string>;
}

function recordConflict(
  base: MaybeAbsent,
  ours: MaybeAbsent,
  theirs: MaybeAbsent,
  path: Path,
  ctx: Ctx,
): MaybeAbsent {
  const key = formatPath(path);
  if (ctx.resolutions.has(key)) {
    ctx.usedResolutions.add(key);
    const resolved = ctx.resolutions.get(key) as JsonValue | Delete;
    return resolved === DELETE ? ABSENT : resolved;
  }
  ctx.conflicts.push({ path, base, ours, theirs });
  if (ours !== ABSENT) return ours;
  if (theirs !== ABSENT) return theirs;
  return ABSENT;
}

function mergeNode(
  base: MaybeAbsent,
  ours: MaybeAbsent,
  theirs: MaybeAbsent,
  path: Path,
  ctx: Ctx,
): MaybeAbsent {
  if (ours === ABSENT && theirs === ABSENT) return ABSENT;
  if (ours !== ABSENT && theirs !== ABSENT && jsonEqual(ours, theirs)) return ours;
  if (base !== ABSENT && ours !== ABSENT && jsonEqual(base, ours)) return theirs;
  if (base !== ABSENT && theirs !== ABSENT && jsonEqual(base, theirs)) return ours;
  if (base === ABSENT && ours === ABSENT) return theirs;
  if (base === ABSENT && theirs === ABSENT) return ours;

  if (
    isPlainObject(ours) &&
    isPlainObject(theirs) &&
    (base === ABSENT || isPlainObject(base))
  ) {
    return mergeObject(base, ours, theirs, path, ctx);
  }
  if (
    Array.isArray(ours) &&
    Array.isArray(theirs) &&
    (base === ABSENT || Array.isArray(base))
  ) {
    return mergeArray(base, ours, theirs, path, ctx);
  }
  return recordConflict(base, ours, theirs, path, ctx);
}

function mergeObject(
  base: MaybeAbsent,
  ours: JsonObject,
  theirs: JsonObject,
  path: Path,
  ctx: Ctx,
): JsonValue {
  const out: JsonObject = {};
  const keys: string[] = [];
  const seen = new Set<string>();
  const sources: (JsonObject | Absent)[] = [base, ours, theirs];
  for (const source of sources) {
    if (source === ABSENT) continue;
    for (const key of Object.keys(source)) {
      if (!seen.has(key)) {
        seen.add(key);
        keys.push(key);
      }
    }
  }
  for (const key of keys) {
    const baseValue =
      base !== ABSENT && hasOwn(base as JsonObject, key)
        ? (base as JsonObject)[key]
        : ABSENT;
    const oursValue = hasOwn(ours, key) ? ours[key] : ABSENT;
    const theirsValue = hasOwn(theirs, key) ? theirs[key] : ABSENT;
    const merged = mergeNode(baseValue, oursValue, theirsValue, [...path, key], ctx);
    if (merged !== ABSENT) out[key] = merged;
  }
  return out;
}

function toIdMap(arr: JsonValue[], idKey: string): Map<string, JsonObject> {
  const map = new Map<string, JsonObject>();
  for (const item of arr) {
    const obj = item as JsonObject;
    map.set(idKeyOf(obj[idKey] as string | number), obj);
  }
  return map;
}

function mergeArray(
  base: MaybeAbsent,
  ours: JsonValue[],
  theirs: JsonValue[],
  path: Path,
  ctx: Ctx,
): JsonValue {
  const baseMap =
    base === ABSENT
      ? new Map<string, JsonObject>()
      : toIdMap(base as JsonValue[], ctx.idKey);
  const oursMap = toIdMap(ours, ctx.idKey);
  const theirsMap = toIdMap(theirs, ctx.idKey);

  const order: string[] = [];
  const seen = new Set<string>();
  const collect = (arr: JsonValue[]) => {
    for (const item of arr) {
      const key = idKeyOf((item as JsonObject)[ctx.idKey] as string | number);
      if (!seen.has(key)) {
        seen.add(key);
        order.push(key);
      }
    }
  };
  if (base !== ABSENT) collect(base as JsonValue[]);
  collect(ours);
  collect(theirs);

  const out: JsonValue[] = [];
  for (const key of order) {
    const baseItem = baseMap.get(key);
    const oursItem = oursMap.get(key);
    const theirsItem = theirsMap.get(key);
    const carrier = oursItem ?? theirsItem ?? baseItem;
    const id = (carrier as JsonObject)[ctx.idKey] as string | number;
    const merged = mergeNode(
      baseItem ?? ABSENT,
      oursItem ?? ABSENT,
      theirsItem ?? ABSENT,
      [...path, { id }],
      ctx,
    );
    if (merged !== ABSENT) out.push(merged);
  }
  return out;
}

export function merge3(
  base: JsonValue,
  ours: JsonValue,
  theirs: JsonValue,
  options: MergeOptions = {},
): MergeResult {
  const idKey = options.idKey ?? "id";
  validateValue(base, [], "base", idKey);
  validateValue(ours, [], "ours", idKey);
  validateValue(theirs, [], "theirs", idKey);

  const resolutions = new Map<string, JsonValue | Delete>();
  const resolutionKey = (r: Resolution): string =>
    typeof r.path === "string" ? r.path : formatPath(r.path);
  for (const resolution of options.resolutions ?? []) {
    resolutions.set(resolutionKey(resolution), resolution.value);
  }

  const ctx: Ctx = {
    idKey,
    conflicts: [],
    resolutions,
    usedResolutions: new Set<string>(),
  };
  const merged = mergeNode(base, ours, theirs, [], ctx);
  const unusedResolutions = (options.resolutions ?? []).filter(
    (r) => !ctx.usedResolutions.has(resolutionKey(r)),
  );
  return {
    tree: merged === ABSENT ? undefined : merged,
    conflicts: ctx.conflicts,
    ok: ctx.conflicts.length === 0,
    unusedResolutions,
  };
}
