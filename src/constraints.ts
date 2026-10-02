/**
 * Numeric constraint model: decimal range + multipleOf + digit limits,
 * normalized from the supported schema subset. All bounds are exact Decimals.
 */

import type { Decimal, DecimalOptions } from "./decimal.ts";
import {
  compareDecimal,
  isMultipleOf,
  parseDecimal,
  precisionOf,
  scaleOf,
  canonical,
} from "./decimal.ts";

export class SchemaError extends Error {
  readonly schemaPath: string;
  constructor(message: string, schemaPath: string = "#") {
    super(message);
    this.name = "SchemaError";
    this.schemaPath = schemaPath;
  }
}

export interface Bound {
  readonly value: Decimal;
  readonly inclusive: boolean;
}

export interface Constraints {
  readonly lo?: Bound;
  readonly hi?: Bound;
  /** Positive exact decimal step, when present. */
  readonly multipleOf?: Decimal;
  /** Max total digits of the numeric value (>= 1). */
  readonly precision?: number;
  /** Max decimal places (>= 0). */
  readonly scale?: number;
}

/** Keywords this library implements. Anything else is rejected, not ignored. */
export const SUPPORTED_NUMBER_KEYWORDS = [
  "minimum",
  "maximum",
  "exclusiveMinimum",
  "exclusiveMaximum",
  "multipleOf",
  "precision",
  "scale",
] as const;

export interface RawNumberSchema {
  minimum?: string | number;
  maximum?: string | number;
  exclusiveMinimum?: string | number;
  exclusiveMaximum?: string | number;
  multipleOf?: string | number;
  precision?: number;
  scale?: number;
}

function asDecimal(
  v: string | number,
  keyword: string,
  schemaPath: string,
  opts: DecimalOptions,
): Decimal {
  try {
    return parseDecimal(v, opts);
  } catch (err) {
    if (err instanceof Error) {
      throw new SchemaError(`${keyword}: ${err.message}`, schemaPath);
    }
    throw err;
  }
}

function asCount(v: unknown, keyword: string, min: number, schemaPath: string): number {
  if (typeof v !== "number" || !Number.isInteger(v) || v < min) {
    throw new SchemaError(`${keyword}: expected an integer >= ${min}, got ${JSON.stringify(v)}`, schemaPath);
  }
  return v;
}

function stricterLo(a: Bound | undefined, b: Bound): Bound {
  if (a === undefined) return b;
  const c = compareDecimal(a.value, b.value);
  if (c < 0) return b;
  if (c > 0) return a;
  return a.inclusive ? b : a; // equal bounds: the exclusive one wins
}

function stricterHi(a: Bound | undefined, b: Bound): Bound {
  if (a === undefined) return b;
  const c = compareDecimal(a.value, b.value);
  if (c > 0) return b;
  if (c < 0) return a;
  return a.inclusive ? b : a;
}

/** Normalize the supported number keywords into a Constraints object. */
export function normalizeConstraints(
  schema: RawNumberSchema,
  opts: DecimalOptions = {},
  schemaPath: string = "#",
): Constraints {
  for (const key of Object.keys(schema)) {
    if (!(SUPPORTED_NUMBER_KEYWORDS as readonly string[]).includes(key)) {
      throw new SchemaError(
        `unsupported keyword ${JSON.stringify(key)}; supported subset: ${SUPPORTED_NUMBER_KEYWORDS.join(", ")}`,
        `${schemaPath}/${key}`,
      );
    }
  }
  let lo: Bound | undefined;
  let hi: Bound | undefined;
  if (schema.minimum !== undefined) {
    lo = stricterLo(lo, { value: asDecimal(schema.minimum, "minimum", `${schemaPath}/minimum`, opts), inclusive: true });
  }
  if (schema.exclusiveMinimum !== undefined) {
    lo = stricterLo(lo, { value: asDecimal(schema.exclusiveMinimum, "exclusiveMinimum", `${schemaPath}/exclusiveMinimum`, opts), inclusive: false });
  }
  if (schema.maximum !== undefined) {
    hi = stricterHi(hi, { value: asDecimal(schema.maximum, "maximum", `${schemaPath}/maximum`, opts), inclusive: true });
  }
  if (schema.exclusiveMaximum !== undefined) {
    hi = stricterHi(hi, { value: asDecimal(schema.exclusiveMaximum, "exclusiveMaximum", `${schemaPath}/exclusiveMaximum`, opts), inclusive: false });
  }
  let multipleOf: Decimal | undefined;
  if (schema.multipleOf !== undefined) {
    const m = asDecimal(schema.multipleOf, "multipleOf", `${schemaPath}/multipleOf`, opts);
    if (m.coeff === 0n) {
      throw new SchemaError("multipleOf must be non-zero", `${schemaPath}/multipleOf`);
    }
    multipleOf = m.neg ? { ...m, neg: false } : m; // sign is irrelevant
  }
  const out: {
    lo?: Bound; hi?: Bound; multipleOf?: Decimal; precision?: number; scale?: number;
  } = {};
  if (lo !== undefined) out.lo = lo;
  if (hi !== undefined) out.hi = hi;
  if (multipleOf !== undefined) out.multipleOf = multipleOf;
  if (schema.precision !== undefined) {
    out.precision = asCount(schema.precision, "precision", 1, `${schemaPath}/precision`);
  }
  if (schema.scale !== undefined) {
    out.scale = asCount(schema.scale, "scale", 0, `${schemaPath}/scale`);
  }
  return out;
}

export interface Violation {
  readonly rule: string;
  readonly message: string;
}

/** Check one exact decimal value against constraints; list every violation. */
export function checkConstraints(v: Decimal, c: Constraints): Violation[] {
  const out: Violation[] = [];
  if (c.lo !== undefined) {
    const cmp = compareDecimal(v, c.lo.value);
    if (cmp < 0 || (cmp === 0 && !c.lo.inclusive)) {
      out.push({
        rule: c.lo.inclusive ? "minimum" : "exclusiveMinimum",
        message: `${canonical(v)} must be ${c.lo.inclusive ? ">= " : "> "}${canonical(c.lo.value)}`,
      });
    }
  }
  if (c.hi !== undefined) {
    const cmp = compareDecimal(v, c.hi.value);
    if (cmp > 0 || (cmp === 0 && !c.hi.inclusive)) {
      out.push({
        rule: c.hi.inclusive ? "maximum" : "exclusiveMaximum",
        message: `${canonical(v)} must be ${c.hi.inclusive ? "<= " : "< "}${canonical(c.hi.value)}`,
      });
    }
  }
  if (c.multipleOf !== undefined && !isMultipleOf(v, c.multipleOf)) {
    out.push({
      rule: "multipleOf",
      message: `${canonical(v)} is not an integer multiple of ${canonical(c.multipleOf)}`,
    });
  }
  if (c.precision !== undefined && precisionOf(v) > c.precision) {
    out.push({
      rule: "precision",
      message: `${canonical(v)} has ${precisionOf(v)} total digits, more than ${c.precision}`,
    });
  }
  if (c.scale !== undefined && scaleOf(v) > c.scale) {
    out.push({
      rule: "scale",
      message: `${canonical(v)} has ${scaleOf(v)} decimal places, more than ${c.scale}`,
    });
  }
  return out;
}
