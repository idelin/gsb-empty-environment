/**
 * Schema subset. Only the keywords below are implemented and promised;
 * anything else is rejected loudly instead of being silently ignored.
 * All numeric bounds are decimal *strings* so negative numbers and
 * scientific exponents never pass through binary floating point.
 */

import type { Decimal, DecimalOptions } from "./decimal.ts";
import { DEFAULT_MAX_SCALE, parseDecimal, compare, DecimalBudgetError, DecimalParseError } from "./decimal.ts";

export interface DecimalSchema {
  minimum?: string;
  maximum?: string;
  exclusiveMinimum?: string;
  exclusiveMaximum?: string;
  multipleOf?: string;
  maxTotalDigits?: number;
  maxFractionDigits?: number;
}

export const SUPPORTED_KEYWORDS = [
  "minimum",
  "maximum",
  "exclusiveMinimum",
  "exclusiveMaximum",
  "multipleOf",
  "maxTotalDigits",
  "maxFractionDigits",
] as const;

export class SchemaError extends Error {
  readonly rulePath: string;
  constructor(rulePath: string, message: string) {
    super(message);
    this.name = "SchemaError";
    this.rulePath = rulePath;
  }
}

export interface Bound {
  value: Decimal;
  inclusive: boolean;
  keyword: "minimum" | "exclusiveMinimum" | "maximum" | "exclusiveMaximum";
}

export interface CompiledSchema {
  lo?: Bound;
  hi?: Bound;
  /** Always stored as a positive value. */
  multipleOf?: Decimal;
  maxTotalDigits?: number;
  maxFractionDigits?: number;
}

export function compileSchema(schema: DecimalSchema, options: DecimalOptions = {}): CompiledSchema {
  const maxScale = options.maxScale ?? DEFAULT_MAX_SCALE;
  if (schema === null || typeof schema !== "object" || Array.isArray(schema)) {
    throw new SchemaError("", "schema must be an object");
  }
  for (const key of Object.keys(schema)) {
    if (!(SUPPORTED_KEYWORDS as readonly string[]).includes(key)) {
      throw new SchemaError(
        `/${key}`,
        `unsupported keyword ${JSON.stringify(key)}: only ${SUPPORTED_KEYWORDS.join(", ")} are implemented`,
      );
    }
  }

  const parseBound = (keyword: string, text: unknown): Decimal => {
    if (typeof text !== "string") {
      throw new SchemaError(`/${keyword}`, `${keyword} must be a decimal string, got ${typeof text}`);
    }
    try {
      return parseDecimal(text, options);
    } catch (err) {
      if (err instanceof DecimalParseError || err instanceof DecimalBudgetError) {
        throw new SchemaError(`/${keyword}`, err.message);
      }
      throw err;
    }
  };

  let lo: Bound | undefined;
  let hi: Bound | undefined;
  if (schema.minimum !== undefined) {
    lo = { value: parseBound("minimum", schema.minimum), inclusive: true, keyword: "minimum" };
  }
  if (schema.exclusiveMinimum !== undefined) {
    const bound: Bound = {
      value: parseBound("exclusiveMinimum", schema.exclusiveMinimum),
      inclusive: false,
      keyword: "exclusiveMinimum",
    };
    // Keep the stronger lower bound; on ties the exclusive one wins.
    if (!lo || compare(bound.value, lo.value) >= 0) lo = bound;
  }
  if (schema.maximum !== undefined) {
    hi = { value: parseBound("maximum", schema.maximum), inclusive: true, keyword: "maximum" };
  }
  if (schema.exclusiveMaximum !== undefined) {
    const bound: Bound = {
      value: parseBound("exclusiveMaximum", schema.exclusiveMaximum),
      inclusive: false,
      keyword: "exclusiveMaximum",
    };
    if (!hi || compare(bound.value, hi.value) <= 0) hi = bound;
  }

  let multipleOf: Decimal | undefined;
  if (schema.multipleOf !== undefined) {
    const parsed = parseBound("multipleOf", schema.multipleOf);
    if (parsed.sign === 0) {
      throw new SchemaError("/multipleOf", "multipleOf must be non-zero");
    }
    multipleOf = { ...parsed, sign: 1 };
  }

  const digitCap = (keyword: "maxTotalDigits" | "maxFractionDigits", min: number): number | undefined => {
    const raw = schema[keyword];
    if (raw === undefined) return undefined;
    if (!Number.isInteger(raw) || (raw as number) < min) {
      throw new SchemaError(`/${keyword}`, `${keyword} must be an integer >= ${min}`);
    }
    if ((raw as number) > maxScale) {
      throw new SchemaError(
        `/${keyword}`,
        `${keyword}=${raw} exceeds the scale budget +/-${maxScale}; refusing before allocating 10^${raw}`,
      );
    }
    return raw as number;
  };

  const compiled: CompiledSchema = {};
  if (lo) compiled.lo = lo;
  if (hi) compiled.hi = hi;
  if (multipleOf) compiled.multipleOf = multipleOf;
  const total = digitCap("maxTotalDigits", 1);
  const frac = digitCap("maxFractionDigits", 0);
  if (total !== undefined) compiled.maxTotalDigits = total;
  if (frac !== undefined) compiled.maxFractionDigits = frac;
  return compiled;
}
