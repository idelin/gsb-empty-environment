import type { Value } from "./types.ts";

export function tupleKey(tuple: readonly Value[]): string {
  return JSON.stringify(tuple);
}

function valueRank(v: Value): number {
  if (typeof v === "number") return 0;
  if (typeof v === "string") return 1;
  return 2;
}

export function compareValues(a: Value, b: Value): number {
  if (typeof a === "number" && typeof b === "number") return a - b;
  if (typeof a === "string" && typeof b === "string") return a < b ? -1 : a > b ? 1 : 0;
  if (typeof a === "boolean" && typeof b === "boolean") return (a ? 1 : 0) - (b ? 1 : 0);
  return valueRank(a) - valueRank(b);
}

export function compareTuples(a: readonly Value[], b: readonly Value[]): number {
  const n = Math.min(a.length, b.length);
  for (let i = 0; i < n; i++) {
    const c = compareValues(a[i], b[i]);
    if (c !== 0) return c;
  }
  return a.length - b.length;
}

export function formatValue(v: Value): string {
  return typeof v === "string" ? JSON.stringify(v) : String(v);
}

export function formatTuple(t: readonly Value[]): string {
  return "(" + t.map(formatValue).join(", ") + ")";
}
