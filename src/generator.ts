import type { Schema } from "./schema.ts";
import { randInt, pick, type Rng } from "./rng.ts";
import { validate } from "./validator.ts";
import { parsePattern, samplePattern } from "./regex-lite.ts";

/**
 * Positive-example generator. Constraints on the same node are solved
 * together (linked constraints): e.g. an integer that must satisfy
 * minimum + maximum + multipleOf simultaneously. When the constraints
 * are contradictory, or a pattern is outside the supported generation
 * subset, it returns a reason instead of fabricating a value.
 */
export type GenResult = { ok: true; value: unknown } | { ok: false; reason: string };

const ok = (value: unknown): GenResult => ({ ok: true, value });
const fail = (reason: string): GenResult => ({ ok: false, reason });

const DEFAULT_BOUND = 100;
const EPS = 1e-9;
const MAX_DEPTH = 12;

export function without(schema: Schema, ...keys: string[]): Schema {
  const copy: Record<string, unknown> = { ...schema };
  for (const k of keys) delete copy[k];
  return copy as Schema;
}

export function generate(schema: Schema, rng: Rng): GenResult {
  return genNode(schema, rng, 0);
}

function genNode(schema: Schema, rng: Rng, depth: number): GenResult {
  if (depth > MAX_DEPTH) return fail(`schema nesting deeper than ${MAX_DEPTH} levels`);

  if (schema.const !== undefined) {
    const errs = validate(schema.const, without(schema, "const"));
    if (errs.length > 0)
      return fail(
        `const ${JSON.stringify(schema.const)} conflicts with ${errs[0].schemaPath} (${errs[0].message})`,
      );
    return ok(schema.const);
  }

  if (schema.enum !== undefined) {
    const rest = without(schema, "enum");
    const survivors = schema.enum.filter((v) => validate(v, rest).length === 0);
    if (survivors.length === 0)
      return fail(`no enum value satisfies the remaining constraints of this node`);
    return ok(pick(rng, survivors));
  }

  switch (resolveType(schema, rng)) {
    case "number":
      return genNumber(schema, rng, false);
    case "integer":
      return genNumber(schema, rng, true);
    case "string":
      return genString(schema, rng);
    case "boolean":
      return ok(pick(rng, [true, false]));
    case "null":
      return ok(null);
    case "array":
      return genArray(schema, rng, depth);
    case "object":
      return genObject(schema, rng, depth);
    default:
      return fail("unreachable");
  }
}

function resolveType(schema: Schema, rng: Rng): string {
  if (schema.type !== undefined) return schema.type;
  if (
    schema.minimum !== undefined ||
    schema.maximum !== undefined ||
    schema.exclusiveMinimum !== undefined ||
    schema.exclusiveMaximum !== undefined ||
    schema.multipleOf !== undefined
  )
    return "number";
  if (schema.minLength !== undefined || schema.maxLength !== undefined || schema.pattern !== undefined)
    return "string";
  if (schema.items !== undefined || schema.minItems !== undefined || schema.maxItems !== undefined)
    return "array";
  if (
    schema.properties !== undefined ||
    schema.required !== undefined ||
    schema.additionalProperties !== undefined
  )
    return "object";
  return pick(rng, ["string", "number", "boolean", "null"]);
}

interface Bounds {
  lo: number;
  loInclusive: boolean;
  hi: number;
  hiInclusive: boolean;
}

export function numericBounds(schema: Schema): Bounds {
  let lo = -Infinity;
  let loInclusive = true;
  let hi = Infinity;
  let hiInclusive = true;
  if (schema.minimum !== undefined && schema.minimum > lo) {
    lo = schema.minimum;
    loInclusive = true;
  }
  if (
    schema.exclusiveMinimum !== undefined &&
    (schema.exclusiveMinimum > lo || (schema.exclusiveMinimum === lo && loInclusive))
  ) {
    lo = schema.exclusiveMinimum;
    loInclusive = false;
  }
  if (schema.maximum !== undefined && schema.maximum < hi) {
    hi = schema.maximum;
    hiInclusive = true;
  }
  if (
    schema.exclusiveMaximum !== undefined &&
    (schema.exclusiveMaximum < hi || (schema.exclusiveMaximum === hi && hiInclusive))
  ) {
    hi = schema.exclusiveMaximum;
    hiInclusive = false;
  }
  return { lo, loInclusive, hi, hiInclusive };
}

function describeBounds(b: Bounds): string {
  return `${b.loInclusive ? "[" : "("}${b.lo}, ${b.hi}${b.hiInclusive ? "]" : ")"}`;
}

function genNumber(schema: Schema, rng: Rng, integer: boolean): GenResult {
  const b = numericBounds(schema);
  if (b.lo > b.hi || (b.lo === b.hi && !(b.loInclusive && b.hiInclusive)))
    return fail(`empty numeric range ${describeBounds(b)}: lower and upper bounds contradict each other`);

  const m = schema.multipleOf;
  if (m !== undefined) {
    if (!(m > 0)) return fail(`multipleOf must be positive, got ${m}`);
    if (integer && !Number.isInteger(m))
      return fail(`cannot construct an integer that is a multiple of non-integer ${m}`);
    const kLo = b.lo === -Infinity ? -1000 : b.loInclusive ? Math.ceil(b.lo / m - EPS) : Math.ceil(b.lo / m + EPS);
    const kHi = b.hi === Infinity ? 1000 : b.hiInclusive ? Math.floor(b.hi / m + EPS) : Math.floor(b.hi / m - EPS);
    if (kLo > kHi)
      return fail(`no multiple of ${m} exists within ${describeBounds(b)}; constraints are contradictory`);
    for (let attempt = 0; attempt < 10; attempt++) {
      const v = randInt(rng, kLo, kHi) * m;
      if (validate(v, schema).length === 0) return ok(v);
    }
    return fail(`could not construct a multiple of ${m} satisfying all numeric constraints`);
  }

  if (integer) {
    const a = b.lo === -Infinity ? -DEFAULT_BOUND : b.loInclusive ? Math.ceil(b.lo - EPS) : Math.ceil(b.lo + EPS);
    const bHi = b.hi === Infinity ? DEFAULT_BOUND : b.hiInclusive ? Math.floor(b.hi + EPS) : Math.floor(b.hi - EPS);
    if (a > bHi) return fail(`no integer exists within ${describeBounds(b)}`);
    return ok(randInt(rng, a, bHi));
  }

  const a = b.lo === -Infinity ? -DEFAULT_BOUND : b.lo;
  const z = b.hi === Infinity ? DEFAULT_BOUND : b.hi;
  for (let attempt = 0; attempt < 20; attempt++) {
    const v = a + rng() * (z - a);
    if (validate(v, schema).length === 0) return ok(v);
  }
  return fail(`could not sample a number satisfying all numeric constraints in ${describeBounds(b)}`);
}

function genString(schema: Schema, rng: Rng): GenResult {
  const minL = schema.minLength ?? 0;
  const maxL = schema.maxLength ?? Math.max(minL, 12);
  if (minL > maxL) return fail(`minLength ${minL} exceeds maxLength ${maxL}; constraints are contradictory`);

  if (schema.pattern !== undefined) {
    const pieces = parsePattern(schema.pattern);
    if (pieces === null)
      return fail(
        `pattern ${JSON.stringify(schema.pattern)} is outside the supported generation subset ` +
          `(anchored ^...$ with literals, [...] classes, \\d \\w \\s, ".", quantifiers, literal alternation)`,
      );
    const re = new RegExp(schema.pattern);
    for (let attempt = 0; attempt < 60; attempt++) {
      const s = samplePattern(pieces, rng, { min: minL, max: maxL });
      if (s.length >= minL && s.length <= maxL && re.test(s) && validate(s, schema).length === 0)
        return ok(s);
    }
    return fail(
      `could not build a string of length ${minL}..${maxL} matching pattern ${JSON.stringify(schema.pattern)}`,
    );
  }

  const alphabet = "abcdefghijklmnopqrstuvwxyz";
  const len = randInt(rng, minL, maxL);
  let s = "";
  for (let i = 0; i < len; i++) s += alphabet[randInt(rng, 0, alphabet.length - 1)];
  return ok(s);
}

function genArray(schema: Schema, rng: Rng, depth: number): GenResult {
  const minI = schema.minItems ?? 0;
  const maxI = schema.maxItems ?? (schema.items ? Math.max(minI + 2, 1) : minI);
  if (minI > maxI) return fail(`minItems ${minI} exceeds maxItems ${maxI}; constraints are contradictory`);
  const loLen = schema.items !== undefined && maxI >= 1 ? Math.max(1, minI) : minI;
  const len = randInt(rng, loLen, Math.max(loLen, maxI));
  const arr: unknown[] = [];
  for (let i = 0; i < len; i++) {
    const r = genNode(schema.items ?? {}, rng, depth + 1);
    if (!r.ok) return fail(`items[${i}]: ${r.reason}`);
    arr.push(r.value);
  }
  return ok(arr);
}

function genObject(schema: Schema, rng: Rng, depth: number): GenResult {
  const props = schema.properties ?? {};
  const obj: Record<string, unknown> = {};
  const keys = [...Object.keys(props), ...(schema.required ?? []).filter((k) => !(k in props))];
  for (const key of keys) {
    const sub = props[key];
    if (sub !== undefined) {
      const r = genNode(sub, rng, depth + 1);
      if (!r.ok) return fail(`property "${key}": ${r.reason}`);
      obj[key] = r.value;
    } else {
      const r = genNode({}, rng, depth + 1);
      obj[key] = r.ok ? r.value : null;
    }
  }
  return ok(obj);
}
