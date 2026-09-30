// A cube is a conjunction of per-attribute constraints: a closed integer
// interval per integer attribute and a value subset per enum attribute.
// A missing key means "unconstrained" for that attribute.

import type { Point, Schema } from './types.ts';
import {
  intersectInterval,
  isEmpty,
  subtractInterval,
  type Interval,
} from './interval.ts';

export interface Cube {
  ints: Map<string, Interval>;
  sets: Map<string, ReadonlySet<string>>;
}

export function emptyCube(): Cube {
  return { ints: new Map(), sets: new Map() };
}

export function intersectCubes(a: Cube, b: Cube): Cube | null {
  const ints = new Map(a.ints);
  for (const [attr, interval] of b.ints) {
    const cur = ints.get(attr);
    const next = cur ? intersectInterval(cur, interval) : interval;
    if (isEmpty(next)) return null;
    ints.set(attr, next);
  }
  const sets = new Map(a.sets);
  for (const [attr, values] of b.sets) {
    const cur = sets.get(attr);
    const next = cur ? new Set([...cur].filter((v) => values.has(v))) : values;
    if (next.size === 0) return null;
    sets.set(attr, next);
  }
  return { ints, sets };
}

/**
 * a \ b as a list of pairwise-disjoint cubes (standard cube difference:
 * for each attribute in `order`, emit the part of `a` that escapes `b` on
 * that attribute while matching a∩b on all earlier attributes).
 */
export function subtractCube(
  a: Cube,
  b: Cube,
  schema: Schema,
  order: readonly string[],
): Cube[] {
  if (!intersectCubes(a, b)) return [a];
  const pieces: Cube[] = [];
  const prefixInts = new Map<string, Interval>();
  const prefixSets = new Map<string, ReadonlySet<string>>();
  for (const attr of order) {
    const def = schema[attr];
    if (def.kind === 'int') {
      const dom: Interval = { lo: def.min, hi: def.max };
      const aI = a.ints.get(attr) ?? dom;
      const bI = b.ints.get(attr) ?? dom;
      for (const d of subtractInterval(aI, bI)) {
        pieces.push({
          ints: new Map([...a.ints, ...prefixInts, [attr, d]]),
          sets: new Map([...a.sets, ...prefixSets]),
        });
      }
      const inter = intersectInterval(aI, bI);
      if (!isEmpty(inter)) prefixInts.set(attr, inter);
    } else {
      const universe: ReadonlySet<string> = new Set(def.values);
      const aS = a.sets.get(attr) ?? universe;
      const bS = b.sets.get(attr) ?? universe;
      const diff = new Set([...aS].filter((v) => !bS.has(v)));
      if (diff.size > 0) {
        pieces.push({
          ints: new Map([...a.ints, ...prefixInts]),
          sets: new Map([...a.sets, ...prefixSets, [attr, diff]]),
        });
      }
      const inter = new Set([...aS].filter((v) => bS.has(v)));
      if (inter.size > 0) prefixSets.set(attr, inter);
    }
  }
  return pieces;
}

/** True when `region` is fully covered by the union of `cubes`. */
export function isCoveredBy(
  region: Cube,
  cubes: readonly Cube[],
  schema: Schema,
  order: readonly string[],
): boolean {
  let rest = [region];
  for (const cube of cubes) {
    rest = rest.flatMap((r) => subtractCube(r, cube, schema, order));
    if (rest.length === 0) return true;
  }
  return false;
}

export function containsPoint(cube: Cube, point: Point): boolean {
  for (const [attr, interval] of cube.ints) {
    const v = point[attr];
    if (typeof v !== 'number' || v < interval.lo || v > interval.hi) return false;
  }
  for (const [attr, values] of cube.sets) {
    if (!values.has(point[attr] as string)) return false;
  }
  return true;
}

/** Lexicographically smallest point of the cube w.r.t. `order` — the minimal counterexample. */
export function witnessOf(cube: Cube, schema: Schema, order: readonly string[]): Point {
  const point: Point = {};
  for (const attr of order) {
    const def = schema[attr];
    if (def.kind === 'int') {
      point[attr] = cube.ints.get(attr)?.lo ?? def.min;
    } else {
      const set = cube.sets.get(attr);
      point[attr] = set
        ? def.values.find((v) => set.has(v))!
        : def.values[0];
    }
  }
  return point;
}

export function describeCube(
  cube: Cube,
  schema: Schema,
  order: readonly string[],
): Record<string, string> {
  const desc: Record<string, string> = {};
  for (const attr of order) {
    const def = schema[attr];
    if (def.kind === 'int') {
      const i = cube.ints.get(attr);
      if (!i || (i.lo === def.min && i.hi === def.max)) desc[attr] = '*';
      else if (i.lo === i.hi) desc[attr] = `${i.lo}`;
      else desc[attr] = `${i.lo}..${i.hi}`;
    } else {
      const s = cube.sets.get(attr);
      desc[attr] = !s
        ? '*'
        : def.values.filter((v) => s.has(v)).join('|') || '(empty)';
    }
  }
  return desc;
}
