/**
 * Algebraic model of physical units: a unit is an integer exponent vector
 * over a fixed set of base dimensions (e.g. L, M, T).
 *
 *   mul(a, b)  = a + b          (componentwise)
 *   div(a, b)  = a - b
 *   pow(a, n)  = n * a          (n integer, may be negative)
 *   dimensionless = zero vector
 */

export type Unit = ReadonlyMap<string, number>;

export const DIMENSIONLESS: Unit = new Map();

export function unitOf(entries: Record<string, number>): Unit {
  const m = new Map<string, number>();
  for (const [dim, exp] of Object.entries(entries)) {
    if (exp !== 0) m.set(dim, exp);
  }
  return m;
}

export function mulUnits(a: Unit, b: Unit): Unit {
  const out = new Map(a);
  for (const [dim, exp] of b) {
    const next = (out.get(dim) ?? 0) + exp;
    if (next === 0) out.delete(dim);
    else out.set(dim, next);
  }
  return out;
}

export function divUnits(a: Unit, b: Unit): Unit {
  const out = new Map(a);
  for (const [dim, exp] of b) {
    const next = (out.get(dim) ?? 0) - exp;
    if (next === 0) out.delete(dim);
    else out.set(dim, next);
  }
  return out;
}

export function powUnit(a: Unit, n: number): Unit {
  if (!Number.isInteger(n)) {
    throw new Error(`exponent must be an integer, got ${n}`);
  }
  const out = new Map<string, number>();
  for (const [dim, exp] of a) {
    const next = exp * n;
    if (next !== 0) out.set(dim, next);
  }
  return out;
}

export function unitsEqual(a: Unit, b: Unit): boolean {
  if (a.size !== b.size) return false;
  for (const [dim, exp] of a) {
    if (b.get(dim) !== exp) return false;
  }
  return true;
}

export function isDimensionless(u: Unit): boolean {
  return u.size === 0;
}

export function formatUnit(u: Unit): string {
  if (u.size === 0) return "1";
  const dims = [...u.keys()].sort();
  const num: string[] = [];
  const den: string[] = [];
  for (const dim of dims) {
    const exp = u.get(dim)!;
    const term = exp === 1 || exp === -1 ? dim : `${dim}^${Math.abs(exp)}`;
    if (exp > 0) num.push(term);
    else den.push(term);
  }
  if (den.length === 0) return num.join("*");
  const denStr = den.length === 1 ? den[0]! : `(${den.join("*")})`;
  if (num.length === 0) return `1/${denStr}`;
  return `${num.join("*")}/${denStr}`;
}
