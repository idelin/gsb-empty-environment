// A dimension is an exponent vector over the declared base units,
// e.g. velocity = { m: 1, s: -1 }. Zero entries are normalized away,
// so the dimensionless dimension is the empty (zero) vector.
export type Dim = Readonly<Record<string, number>>;

export const DIMENSIONLESS: Dim = {};

export function unitDim(base: string): Dim {
  return { [base]: 1 };
}

function normalize(entries: Iterable<readonly [string, number]>): Dim {
  const out: Record<string, number> = {};
  for (const [key, value] of entries) {
    if (value !== 0) out[key] = value;
  }
  return out;
}

function combine(a: Dim, b: Dim, f: (x: number, y: number) => number): Dim {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
  return normalize(
    [...keys].map((k) => [k, f(a[k] ?? 0, b[k] ?? 0)] as const),
  );
}

export function dimMul(a: Dim, b: Dim): Dim {
  return combine(a, b, (x, y) => x + y);
}

export function dimDiv(a: Dim, b: Dim): Dim {
  return combine(a, b, (x, y) => x - y);
}

export function dimPow(a: Dim, exponent: number): Dim {
  return normalize(Object.entries(a).map(([k, v]) => [k, v * exponent] as const));
}

export function dimEqual(a: Dim, b: Dim): boolean {
  const aKeys = Object.keys(a);
  const bKeys = Object.keys(b);
  return aKeys.length === bKeys.length && aKeys.every((k) => a[k] === b[k]);
}

export function isDimensionless(d: Dim): boolean {
  return Object.keys(d).length === 0;
}

export function formatDim(d: Dim): string {
  const keys = Object.keys(d).sort();
  if (keys.length === 0) return "1";
  const factor = (k: string, e: number): string => (e === 1 ? k : `${k}^${e}`);
  const numerator = keys
    .filter((k) => d[k] > 0)
    .map((k) => factor(k, d[k]))
    .join("*");
  const denominator = keys
    .filter((k) => d[k] < 0)
    .map((k) => factor(k, -d[k]))
    .join("*");
  if (denominator === "") return numerator;
  return `${numerator === "" ? "1" : numerator}/${denominator}`;
}
