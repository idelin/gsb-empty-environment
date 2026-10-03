import {
  ALL_VALUE_TYPES,
  effectiveEnum,
  has,
  stableStringify,
  valueTypes,
  type JsonSchema,
  type SchemaObject,
  type ValueType,
} from "./schema.ts";
import { accepts, acceptsNonEnum } from "./evaluate.ts";
import {
  findNonInteger,
  intBounds,
  intervalContains,
  lenInterval,
  numInterval,
  sizeInterval,
  type Interval,
} from "./intervals.ts";

export interface Maybe<T> {
  ok: boolean;
  value?: T;
}

const ok = <T>(value: T): Maybe<T> => ({ ok: true, value });
const fail = <T>(): Maybe<T> => ({ ok: false });

/** The k-th distinct string of exactly `len` code points; null when exhausted. */
export function stringOfLength(len: number, k: number): string | null {
  if (len === 0) return k === 0 ? "" : null;
  let cp = 0x100 + k;
  if (cp >= 0xd800) cp += 0x800; // skip UTF-16 surrogate range
  if (cp > 0x10ffff) return null;
  return "a".repeat(len - 1) + String.fromCodePoint(cp);
}

/** Construct one value of the given value-type that the schema accepts. */
export function makeOfType(s: SchemaObject, vt: ValueType): Maybe<unknown> {
  switch (vt) {
    case "null":
      return ok(null);
    case "boolean":
      return ok(true);
    case "integer": {
      const { lo, hi } = intBounds(numInterval(s));
      if (lo > hi) return fail();
      const v = lo <= 0 && hi >= 0 ? 0 : lo === -Infinity ? hi : lo;
      return ok(v);
    }
    case "number": {
      const x = findNonInteger(numInterval(s));
      return x === undefined ? fail() : ok(x);
    }
    case "string": {
      const { lo, hi } = intBounds(lenInterval(s));
      if (lo > hi) return fail();
      return ok(stringOfLength(lo, 0) ?? "");
    }
    case "array": {
      const { lo, hi } = intBounds(sizeInterval(s));
      if (lo > hi) return fail();
      if (lo === 0) return ok([]);
      const item = makeInside(s.items ?? true);
      if (!item.ok) return fail();
      return ok(new Array(lo).fill(item.value));
    }
    case "object":
      return buildObject(s);
  }
}

/** Minimal object satisfying `required` (no optional/extra keys). */
export function buildObject(s: SchemaObject): Maybe<Record<string, unknown>> {
  const props = s.properties ?? {};
  const obj: Record<string, unknown> = {};
  for (const r of s.required ?? []) {
    const sub: JsonSchema | undefined = has(props, r)
      ? props[r]
      : s.additionalProperties === false
        ? undefined
        : (s.additionalProperties ?? true);
    if (sub === undefined) return fail();
    const v = makeInside(sub);
    if (!v.ok) return fail();
    obj[r] = v.value;
  }
  return ok(obj);
}

/** Construct one value the schema accepts, if any exist. */
export function makeInside(schema: JsonSchema): Maybe<unknown> {
  if (schema === false) return fail();
  if (schema === true) return ok(null);
  const en = effectiveEnum(schema);
  if (en !== undefined) {
    for (const v of en) if (acceptsNonEnum(schema, v)) return ok(v);
    return fail();
  }
  const types = valueTypes(schema);
  for (const vt of ALL_VALUE_TYPES) {
    if (!types.has(vt)) continue;
    const r = makeOfType(schema, vt);
    if (r.ok) return r;
  }
  return fail();
}

/** Construct one value the schema rejects, if any exist. */
export function makeOutside(schema: JsonSchema): Maybe<unknown> {
  if (schema === true) return fail();
  if (schema === false) return ok(null);
  const s = schema;
  const en = effectiveEnum(s);
  if (en !== undefined) {
    const pool: unknown[] = [null, true, false, 0, 1, -1, 0.5, "", "x", [], [0], {}, { a: 0 }];
    for (const c of pool) if (!accepts(s, c)) return ok(c);
    for (let k = 0; k <= en.length + 2; k++) {
      const c = "s" + k;
      if (!accepts(s, c)) return ok(c);
    }
    return fail();
  }
  const types = valueTypes(s);
  for (const vt of ALL_VALUE_TYPES) {
    if (!types.has(vt)) {
      const v = makeOfType({}, vt);
      if (v.ok) return v;
    }
  }
  const iv = numInterval(s);
  if (types.has("integer")) {
    if (iv.max !== undefined) return ok(iv.maxExcl === true ? Math.ceil(iv.max) : Math.floor(iv.max) + 1);
    if (iv.min !== undefined) return ok(iv.minExcl === true ? Math.floor(iv.min) : Math.ceil(iv.min) - 1);
  }
  if (types.has("number")) {
    if (iv.max !== undefined) return ok(iv.max + 0.5);
    if (iv.min !== undefined) return ok(iv.min - 0.5);
  }
  if (types.has("string")) {
    if (s.maxLength !== undefined) return ok("a".repeat(s.maxLength + 1));
    if (s.minLength !== undefined && s.minLength > 0) return ok("");
  }
  if (types.has("array")) {
    if (s.maxItems !== undefined) {
      const item = makeInside(s.items ?? true);
      if (item.ok) return ok(new Array(s.maxItems + 1).fill(item.value));
      return ok([null]); // items accept nothing: any non-empty array is rejected
    }
    if (s.minItems !== undefined && s.minItems > 0) return ok([]);
    if (s.items !== undefined) {
      const w = makeOutside(s.items);
      if (w.ok) {
        const size = Math.max(1, intBounds(sizeInterval(s)).lo);
        const fill = makeInside(s.items);
        const arr = new Array(size).fill(fill.ok ? fill.value : null);
        arr[0] = w.value;
        return ok(arr);
      }
    }
  }
  if (types.has("object")) {
    if ((s.required ?? []).length > 0) return ok({});
    const baseR = buildObject(s);
    const base = baseR.ok ? baseR.value! : {};
    if (s.additionalProperties === false) return ok({ ...base, __extra_0: null });
    for (const [k, sub] of Object.entries(s.properties ?? {})) {
      const w = makeOutside(sub as JsonSchema);
      if (w.ok) return ok({ ...base, [k]: w.value });
    }
    if (s.additionalProperties !== undefined && s.additionalProperties !== true) {
      const w = makeOutside(s.additionalProperties);
      if (w.ok) return ok({ ...base, __extra_0: w.value });
    }
  }
  return fail();
}

function* enumIntegers(iv: Interval): Generator<number> {
  const { lo, hi } = intBounds(iv);
  if (lo > hi) return;
  if (lo === -Infinity && hi === Infinity) {
    yield 0;
    for (let k = 1; ; k++) {
      yield k;
      yield -k;
    }
  } else if (lo === -Infinity) {
    for (let v = hi; ; v--) yield v;
  } else if (hi === Infinity) {
    for (let v = lo; ; v++) yield v;
  } else {
    for (let v = lo; v <= hi; v++) yield v;
  }
}

function* enumNonIntegers(iv: Interval): Generator<number> {
  const lo = iv.min ?? -Infinity;
  const hi = iv.max ?? Infinity;
  if (lo > hi || (lo === hi && (iv.minExcl === true || iv.maxExcl === true))) return;
  if (lo === hi) {
    if (!Number.isInteger(lo)) yield lo;
    return;
  }
  if (Number.isFinite(lo) && Number.isFinite(hi)) {
    const seen = new Set<number>();
    for (let d = 2; ; d *= 2) {
      for (let j = 1; j < d; j++) {
        const c = lo + ((hi - lo) * j) / d;
        if (seen.has(c) || Number.isInteger(c) || !intervalContains(iv, c)) continue;
        seen.add(c);
        yield c;
      }
    }
  } else if (Number.isFinite(lo)) {
    for (let k = 0; ; k++) {
      const c = Math.ceil(lo) + 0.5 + k;
      if (intervalContains(iv, c)) yield c;
    }
  } else if (Number.isFinite(hi)) {
    for (let k = 0; ; k++) {
      const c = Math.floor(hi) - 0.5 - k;
      if (intervalContains(iv, c)) yield c;
    }
  } else {
    for (let k = 0; ; k++) {
      yield k + 0.5;
      yield -(k + 0.5);
    }
  }
}

function* enumStrings(iv: Interval): Generator<string> {
  const { lo, hi } = intBounds(iv);
  if (lo > hi) return;
  if (hi !== Infinity) {
    for (let k = 0; ; k++) {
      const s = stringOfLength(lo, k);
      if (s === null) return;
      yield s;
    }
  } else {
    if (lo === 0) yield "";
    for (let len = Math.max(lo, 1); ; len++) {
      for (let j = 0; j < 8; j++) {
        const s = stringOfLength(len, j);
        if (s !== null) yield s;
      }
    }
  }
}

function* tuples(vals: unknown[], size: number): Generator<unknown[]> {
  if (size === 0) {
    yield [];
    return;
  }
  if (vals.length === 0) return;
  const idx = new Array<number>(size).fill(0);
  for (;;) {
    yield idx.map((i) => vals[i]);
    let p = size - 1;
    while (p >= 0) {
      idx[p]++;
      if (idx[p] < vals.length) break;
      idx[p] = 0;
      p--;
    }
    if (p < 0) return;
  }
}

function* enumArrays(s: SchemaObject): Generator<unknown[]> {
  const { lo, hi } = intBounds(sizeInterval(s));
  if (lo > hi) return;
  const items: JsonSchema = s.items ?? true;
  const first = makeInside(items);
  if (!first.ok) {
    if (lo <= 0) yield [];
    return;
  }
  const enumVals =
    typeof items === "object" && items !== null && !Array.isArray(items)
      ? effectiveEnum(items)?.filter((v) => acceptsNonEnum(items, v))
      : undefined;
  const sizes: Generator<number> =
    hi === Infinity
      ? (function* () {
          for (let v = lo; ; v++) yield v;
        })()
      : (function* () {
          for (let v = lo; v <= hi; v++) yield v;
        })();
  for (const size of sizes) {
    if (size === 0) {
      yield [];
      continue;
    }
    if (enumVals !== undefined) {
      yield* tuples(enumVals, size);
    } else {
      for (const w of enumerateValues(items)) {
        const arr = new Array<unknown>(size).fill(first.value);
        arr[0] = w;
        yield arr;
      }
    }
  }
}

const ABSENT: unique symbol = Symbol("absent");

function* enumObjects(s: SchemaObject): Generator<Record<string, unknown>> {
  const baseR = buildObject(s);
  if (!baseR.ok) return;
  const base = baseR.value!;
  const seen = new Set<string>([stableStringify(base)]);
  yield base;
  const props = s.properties ?? {};
  const ap = s.additionalProperties;
  if (ap !== false) {
    const w = makeInside(ap ?? true);
    if (w.ok) {
      for (let k = 0; ; k++) {
        const obj = { ...base, ["__extra_" + k]: w.value };
        const key = stableStringify(obj);
        if (!seen.has(key)) {
          seen.add(key);
          yield obj;
        }
      }
    }
  }
  const keys = Object.keys(props);
  const req = new Set(s.required ?? []);
  function* options(k: string): Generator<unknown> {
    if (!req.has(k)) yield ABSENT;
    yield* enumerateValues(props[k]);
  }
  function* rec(i: number, acc: Record<string, unknown>): Generator<Record<string, unknown>> {
    if (i === keys.length) {
      yield acc;
      return;
    }
    for (const v of options(keys[i])) {
      const next = { ...acc };
      if (v === ABSENT) delete next[keys[i]];
      else next[keys[i]] = v;
      yield* rec(i + 1, next);
    }
  }
  for (const obj of rec(0, { ...base })) {
    const key = stableStringify(obj);
    if (seen.has(key)) continue;
    seen.add(key);
    yield obj;
  }
}

/**
 * Lazily enumerate DISTINCT values the schema accepts. Terminates iff the
 * accepted set is finite. Only distinctness and termination-when-finite are
 * relied upon by the compatibility checker.
 */
export function* enumerateValues(schema: JsonSchema): Generator<unknown> {
  if (schema === false) return;
  if (schema === true) {
    yield* enumerateValues({});
    return;
  }
  const en = effectiveEnum(schema);
  if (en !== undefined) {
    const seen = new Set<string>();
    for (const v of en) {
      if (!acceptsNonEnum(schema, v)) continue;
      const key = stableStringify(v);
      if (seen.has(key)) continue;
      seen.add(key);
      yield v;
    }
    return;
  }
  const types = valueTypes(schema);
  if (types.has("null")) yield null;
  if (types.has("boolean")) {
    yield true;
    yield false;
  }
  if (types.has("integer")) yield* enumIntegers(numInterval(schema));
  if (types.has("number")) yield* enumNonIntegers(numInterval(schema));
  if (types.has("string")) yield* enumStrings(lenInterval(schema));
  if (types.has("array")) yield* enumArrays(schema);
  if (types.has("object")) yield* enumObjects(schema);
}
