import type { JsonValue } from "./json.ts";
import { TemplateError } from "./errors.ts";
import type { Literal } from "./expr.ts";

export interface FnContext {
  templatePath: string;
  inputPath: string | undefined;
}

/**
 * Whitelisted format function. Receives the current (unwrapped) value, a
 * `missing` flag (true only when the head path did not exist — explicit null
 * is NOT missing), literal args, and an error context.
 */
export type FnImpl = (
  value: JsonValue | undefined,
  missing: boolean,
  args: Literal[],
  ctx: FnContext,
) => JsonValue;

export function isTruthy(v: JsonValue | undefined): boolean {
  if (v === undefined || v === null || v === false) return false;
  if (typeof v === "number") return v !== 0 && !Number.isNaN(v);
  if (typeof v === "string") return v.length > 0;
  return true;
}

export function deepEqual(a: JsonValue | undefined, b: JsonValue | undefined): boolean {
  if (a === b) return true;
  if (a === null || b === null || a === undefined || b === undefined) return false;
  if (typeof a !== "object" || typeof b !== "object") return false;
  if (Array.isArray(a) !== Array.isArray(b)) return false;
  if (Array.isArray(a) && Array.isArray(b)) {
    if (a.length !== b.length) return false;
    return a.every((item, i) => deepEqual(item, b[i]));
  }
  const ao = a as { [k: string]: JsonValue };
  const bo = b as { [k: string]: JsonValue };
  const ak = Object.keys(ao);
  const bk = Object.keys(bo);
  if (ak.length !== bk.length) return false;
  return ak.every((k) => k in bo && deepEqual(ao[k], bo[k]));
}

function describe(v: JsonValue | undefined): string {
  if (v === undefined) return "missing";
  if (v === null) return "null";
  if (Array.isArray(v)) return "array";
  return typeof v;
}

function fail(ctx: FnContext, fn: string, msg: string): TemplateError {
  return new TemplateError(`${fn}: ${msg}`, ctx.templatePath, ctx.inputPath);
}

function needString(v: JsonValue | undefined, ctx: FnContext, fn: string): string {
  if (typeof v !== "string") throw fail(ctx, fn, `expected string, got ${describe(v)}`);
  return v;
}

function needArray(v: JsonValue | undefined, ctx: FnContext, fn: string): JsonValue[] {
  if (!Array.isArray(v)) throw fail(ctx, fn, `expected array, got ${describe(v)}`);
  return v;
}

function stringify(v: JsonValue | undefined): string {
  if (v === undefined || v === null) return "null";
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  return JSON.stringify(v);
}

/** Built-in whitelist. Any function not listed here is rejected at parse time. */
export const builtins: Record<string, FnImpl> = {
  upper: (v, _m, _a, c) => needString(v, c, "upper").toUpperCase(),
  lower: (v, _m, _a, c) => needString(v, c, "lower").toLowerCase(),
  trim: (v, _m, _a, c) => needString(v, c, "trim").trim(),

  /** default(x): substitutes x ONLY when the value is missing; explicit null passes through. */
  default: (v, m, a) => (m ? (a.length > 0 ? a[0] : null) : v === undefined ? null : v),

  json: (v) => JSON.stringify(v === undefined ? null : v),

  length: (v, _m, _a, c) => {
    if (typeof v === "string" || Array.isArray(v)) return v.length;
    if (v !== null && typeof v === "object") return Object.keys(v).length;
    throw fail(c, "length", `expected string/array/object, got ${describe(v)}`);
  },

  join: (v, _m, a, c) => {
    const arr = needArray(v, c, "join");
    const sep = a.length > 0 ? stringify(a[0]) : ",";
    return arr.map((item) => stringify(item)).join(sep);
  },

  first: (v, _m, _a, c) => {
    const arr = needArray(v, c, "first");
    return arr.length > 0 ? arr[0] : null;
  },

  last: (v, _m, _a, c) => {
    const arr = needArray(v, c, "last");
    return arr.length > 0 ? arr[arr.length - 1] : null;
  },

  not: (v) => !isTruthy(v),

  eq: (v, _m, a) => deepEqual(v === undefined ? null : v, a.length > 0 ? a[0] : null),
};
