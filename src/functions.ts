import { deepEqual, typeName, type JsonValue } from "./json.ts";

export class FnError extends Error {}

export type FnImpl = (args: JsonValue[]) => JsonValue;

export function truthy(v: JsonValue): boolean {
  if (v === null || v === false || v === 0 || v === "") return false;
  if (Array.isArray(v)) return v.length > 0;
  return true;
}

function needStr(v: JsonValue | undefined, fn: string, i: number): string {
  if (typeof v !== "string") throw new FnError(`${fn}: argument ${i} must be a string, got ${typeName(v as JsonValue)}`);
  return v;
}
function needNum(v: JsonValue | undefined, fn: string, i: number): number {
  if (typeof v !== "number") throw new FnError(`${fn}: argument ${i} must be a number, got ${typeName(v as JsonValue)}`);
  return v;
}
function needArr(v: JsonValue | undefined, fn: string, i: number): JsonValue[] {
  if (!Array.isArray(v)) throw new FnError(`${fn}: argument ${i} must be an array, got ${typeName(v as JsonValue)}`);
  return v;
}
function toText(v: JsonValue): string {
  if (v === null) return "null";
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}
function arity(args: JsonValue[], fn: string, min: number, max: number): void {
  if (args.length < min || args.length > max) {
    throw new FnError(`${fn}: expects ${min === max ? min : `${min}..${max}`} argument(s), got ${args.length}`);
  }
}

/**
 * The whitelist. Templates may only call functions registered here
 * (plus the special forms default/defined/and/or handled by the evaluator).
 */
export const builtinFunctions: Record<string, FnImpl> = {
  upper: (a) => {
    arity(a, "upper", 1, 1);
    return needStr(a[0], "upper", 0).toUpperCase();
  },
  lower: (a) => {
    arity(a, "lower", 1, 1);
    return needStr(a[0], "lower", 0).toLowerCase();
  },
  trim: (a) => {
    arity(a, "trim", 1, 1);
    return needStr(a[0], "trim", 0).trim();
  },
  len: (a) => {
    arity(a, "len", 1, 1);
    const v = a[0];
    if (typeof v === "string") return v.length;
    if (Array.isArray(v)) return v.length;
    if (v !== null && typeof v === "object") return Object.keys(v).length;
    throw new FnError(`len: cannot take length of ${typeName(v)}`);
  },
  join: (a) => {
    arity(a, "join", 1, 2);
    const arr = needArr(a[0], "join", 0);
    const sep = a.length > 1 ? needStr(a[1], "join", 1) : ",";
    return arr.map(toText).join(sep);
  },
  concat: (a) => a.map(toText).join(""),
  add: (a) => {
    arity(a, "add", 2, 99);
    return a.reduce<number>((acc, v, i) => acc + needNum(v, "add", i), 0);
  },
  sub: (a) => {
    arity(a, "sub", 2, 2);
    return needNum(a[0], "sub", 0) - needNum(a[1], "sub", 1);
  },
  mul: (a) => {
    arity(a, "mul", 2, 99);
    return a.reduce<number>((acc, v, i) => acc * needNum(v, "mul", i), 1);
  },
  div: (a) => {
    arity(a, "div", 2, 2);
    const d = needNum(a[1], "div", 1);
    if (d === 0) throw new FnError("div: division by zero");
    return needNum(a[0], "div", 0) / d;
  },
  mod: (a) => {
    arity(a, "mod", 2, 2);
    const d = needNum(a[1], "mod", 1);
    if (d === 0) throw new FnError("mod: division by zero");
    return needNum(a[0], "mod", 0) % d;
  },
  eq: (a) => {
    arity(a, "eq", 2, 2);
    return deepEqual(a[0], a[1]);
  },
  ne: (a) => {
    arity(a, "ne", 2, 2);
    return !deepEqual(a[0], a[1]);
  },
  gt: (a) => {
    arity(a, "gt", 2, 2);
    return needNum(a[0], "gt", 0) > needNum(a[1], "gt", 1);
  },
  gte: (a) => {
    arity(a, "gte", 2, 2);
    return needNum(a[0], "gte", 0) >= needNum(a[1], "gte", 1);
  },
  lt: (a) => {
    arity(a, "lt", 2, 2);
    return needNum(a[0], "lt", 0) < needNum(a[1], "lt", 1);
  },
  lte: (a) => {
    arity(a, "lte", 2, 2);
    return needNum(a[0], "lte", 0) <= needNum(a[1], "lte", 1);
  },
  not: (a) => {
    arity(a, "not", 1, 1);
    return !truthy(a[0]);
  },
  fmt: (a) => {
    arity(a, "fmt", 1, 99);
    const tpl = needStr(a[0], "fmt", 0);
    return tpl.replace(/\{(\d+)\}/g, (_m, n: string) => {
      const idx = Number(n) + 1;
      if (idx < 1 || idx > a.length - 1) {
        throw new FnError(`fmt: no argument for placeholder {${n}}`);
      }
      return toText(a[idx]);
    });
  },
};
