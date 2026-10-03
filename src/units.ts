/**
 * Units with exact rational conversion factors and dimension vectors.
 * A dimension mismatch is a hard error recorded in the trace; it is never
 * smoothed over by any numeric tolerance.
 */
import { Rational } from "./rational.ts";

/** Dimension vector: base-dimension name -> integer exponent (0s omitted). */
export type Dims = Record<string, number>;

export function dimsKey(d: Dims): string {
  const keys = Object.keys(d).filter((k) => d[k] !== 0).sort();
  return keys.length === 0 ? "1" : keys.map((k) => `${k}^${d[k]}`).join("*");
}

export function dimsEqual(a: Dims, b: Dims): boolean {
  return dimsKey(a) === dimsKey(b);
}

export function dimsMul(a: Dims, b: Dims): Dims {
  const out: Dims = { ...a };
  for (const k of Object.keys(b)) out[k] = (out[k] ?? 0) + b[k];
  for (const k of Object.keys(out)) if (out[k] === 0) delete out[k];
  return out;
}

export function dimsInv(a: Dims): Dims {
  const out: Dims = {};
  for (const k of Object.keys(a)) if (a[k] !== 0) out[k] = -a[k];
  return out;
}

export interface UnitDef {
  name: string;
  dims: Dims;
  /** Exact factor from this unit to the base unit of its dimension. */
  scale: Rational;
}

export type UnitTable = Record<string, UnitDef>;

function u(name: string, dims: Dims, scale: string): [string, UnitDef] {
  return [name, { name, dims, scale: Rational.parse(scale) }];
}

/** Exact SI-flavoured default table. */
export const SI_TABLE: UnitTable = Object.fromEntries([
  u("1", {}, "1"),
  u("m", { m: 1 }, "1"),
  u("km", { m: 1 }, "1000"),
  u("cm", { m: 1 }, "1/100"),
  u("mm", { m: 1 }, "1/1000"),
  u("in", { m: 1 }, "127/5000"), // 0.0254 exactly
  u("s", { s: 1 }, "1"),
  u("min", { s: 1 }, "60"),
  u("h", { s: 1 }, "3600"),
  u("g", { kg: 1 }, "1/1000"),
  u("kg", { kg: 1 }, "1"),
  u("L", { m: 3 }, "1/1000"),
  u("mL", { m: 3 }, "1/1000000"),
  u("percent", {}, "1/100"),
  u("ppm", {}, "1/1000000"),
]);

export function lookupUnit(table: UnitTable, name: string): UnitDef {
  const def = table[name];
  if (!def) throw new Error(`unknown unit ${JSON.stringify(name)}`);
  return def;
}

/** Convert a magnitude expressed in `unit` into base units (exact). */
export function toBase(
  table: UnitTable,
  magnitude: Rational,
  unit: string
): { value: Rational; dims: Dims } {
  const def = lookupUnit(table, unit);
  return { value: magnitude.mul(def.scale), dims: def.dims };
}

/** Re-express a base-unit value as a magnitude in `unit` (exact). */
export function fromBase(table: UnitTable, base: Rational, unit: string): Rational {
  const def = lookupUnit(table, unit);
  return base.div(def.scale);
}
