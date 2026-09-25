// Explicit finite domains and abstract cells (products of intervals / value
// subsets). Containment is decided by refining cells through interval
// splitting instead of enumerating individual values.

import type { MaybeValue, Point, Value } from './conditions.ts';

export type VarDomain =
  | { readonly kind: 'int'; readonly lo: number; readonly hi: number; readonly optional?: boolean }
  | { readonly kind: 'enum'; readonly values: readonly Value[]; readonly optional?: boolean };

export type Domain = Readonly<Record<string, VarDomain>>;

/** A cell component: optional unknown (⊥) plus a concrete interval / value set. */
export type Comp =
  | { readonly kind: 'int'; readonly lo: number; readonly hi: number; readonly unk: boolean }
  | { readonly kind: 'enum'; readonly values: readonly Value[]; readonly unk: boolean };

export type Cell = Readonly<Record<string, Comp>>;

export function domainOrder(domain: Domain): string[] {
  return Object.keys(domain);
}

export function rootCell(domain: Domain): Cell {
  const cell: Record<string, Comp> = {};
  for (const [name, vd] of Object.entries(domain)) {
    const unk = vd.optional === true;
    cell[name] = vd.kind === 'int'
      ? { kind: 'int', lo: vd.lo, hi: vd.hi, unk }
      : { kind: 'enum', values: [...vd.values], unk };
  }
  return cell;
}

function concreteSize(comp: Comp): number {
  return comp.kind === 'int' ? Math.max(0, comp.hi - comp.lo + 1) : comp.values.length;
}

export function compSize(comp: Comp): number {
  return concreteSize(comp) + (comp.unk ? 1 : 0);
}

export function isEmptyComp(comp: Comp): boolean {
  return compSize(comp) === 0;
}

export function isSingletonCell(cell: Cell, order: readonly string[]): boolean {
  return order.every((v) => compSize(cell[v]) === 1);
}

export function cellPoint(cell: Cell, order: readonly string[]): Point {
  const point: Record<string, MaybeValue> = {};
  for (const v of order) {
    const comp = cell[v];
    if (comp.kind === 'int') point[v] = comp.unk && concreteSize(comp) === 0 ? null : comp.lo;
    else point[v] = comp.unk && comp.values.length === 0 ? null : comp.values[0];
  }
  return point;
}

export function cellKey(cell: Cell, order: readonly string[]): string {
  return order.map((v) => {
    const c = cell[v];
    return c.kind === 'int'
      ? `${v}:i:${c.lo}:${c.hi}:${c.unk ? 1 : 0}`
      : `${v}:e:${c.values.join(',')}:${c.unk ? 1 : 0}`;
  }).join('|');
}

function isSplittable(comp: Comp): boolean {
  if (comp.unk && concreteSize(comp) > 0) return true;
  return comp.kind === 'int' ? comp.hi > comp.lo : comp.values.length > 1;
}

/**
 * Split the first splittable variable (in domain order) into a lower and an
 * upper half. Unknown (⊥) peels off first and sorts lowest; integer intervals
 * split at predicate boundary constants when possible, else at the midpoint;
 * enum sets split in declaration order. Returns null when the cell is a
 * singleton or empty.
 */
export function splitCell(
  cell: Cell,
  order: readonly string[],
  boundaries: ReadonlyMap<string, readonly number[]>,
): [Cell, Cell] | null {
  for (const v of order) {
    const comp = cell[v];
    if (!isSplittable(comp)) continue;
    const withComp = (c: Comp): Cell => ({ ...cell, [v]: c });

    if (comp.unk && concreteSize(comp) > 0) {
      const unkOnly: Comp = comp.kind === 'int'
        ? { kind: 'int', lo: 1, hi: 0, unk: true }
        : { kind: 'enum', values: [], unk: true };
      const concreteOnly: Comp = comp.kind === 'int'
        ? { kind: 'int', lo: comp.lo, hi: comp.hi, unk: false }
        : { kind: 'enum', values: comp.values, unk: false };
      return [withComp(unkOnly), withComp(concreteOnly)];
    }

    if (comp.kind === 'int') {
      const { lo, hi } = comp;
      const hints = boundaries.get(v) ?? [];
      let splitAt = -1;
      for (const c of hints) {
        for (const s of [c, c + 1]) {
          if (s > lo && s <= hi && (splitAt === -1 || s < splitAt)) splitAt = s;
        }
      }
      if (splitAt === -1) splitAt = lo + Math.ceil((hi - lo) / 2);
      return [
        withComp({ kind: 'int', lo, hi: splitAt - 1, unk: false }),
        withComp({ kind: 'int', lo: splitAt, hi, unk: false }),
      ];
    }

    const half = Math.ceil(comp.values.length / 2);
    return [
      withComp({ kind: 'enum', values: comp.values.slice(0, half), unk: false }),
      withComp({ kind: 'enum', values: comp.values.slice(half), unk: false }),
    ];
  }
  return null;
}

// ---------------------------------------------------------------- point order

function valueRank(domain: Domain, variable: string, value: MaybeValue): number {
  if (value === null) return -1; // unknown sorts lowest
  const vd = domain[variable];
  if (vd.kind === 'int') return value as number;
  const idx = vd.values.indexOf(value);
  return idx === -1 ? Number.MAX_SAFE_INTEGER : idx;
}

/** Lexicographic order over domain declaration order; ⊥ is smallest. */
export function comparePoints(domain: Domain, order: readonly string[], a: Point, b: Point): number {
  for (const v of order) {
    const ra = valueRank(domain, v, a[v] ?? null);
    const rb = valueRank(domain, v, b[v] ?? null);
    if (ra !== rb) return ra - rb;
  }
  return 0;
}

export function pointKey(point: Point, order: readonly string[]): string {
  return order.map((v) => {
    const val = point[v];
    return val === null || val === undefined ? `${v}=⊥` : `${v}=${String(val)}`;
  }).join(', ');
}

export function formatPoint(point: Point, order: readonly string[]): string {
  return `{${pointKey(point, order).replaceAll('=', ': ')}}`;
}
