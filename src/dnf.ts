// Condition validation and DNF normalization with a shared structural cache,
// so identical subexpressions across rules are solved only once.

import type { Cond, Schema } from './types.ts';
import { complementInterval, type Interval } from './interval.ts';
import { emptyCube, intersectCubes, type Cube } from './cube.ts';

export interface DnfCache {
  cache: Map<string, Cube[]>;
  hits: number;
  misses: number;
}

export function newDnfCache(): DnfCache {
  return { cache: new Map(), hits: 0, misses: 0 };
}

/** Canonical structural key (argument order of and/or is normalized). */
export function condKey(c: Cond): string {
  switch (c.op) {
    case 'const':
      return c.value ? 'T' : 'F';
    case 'in':
      return `in(${c.attr}|${[...c.values].sort().join(',')})`;
    case 'cmp':
      return `cmp(${c.attr}|${c.cmp}|${c.value})`;
    case 'not':
      return `not(${condKey(c.arg)})`;
    case 'and':
    case 'or':
      return `${c.op}(${c.args.map(condKey).sort().join(',')})`;
  }
}

/** Policies may only read explicitly declared attributes, with matching kinds. */
export function validateCond(cond: Cond, schema: Schema): void {
  switch (cond.op) {
    case 'const':
      return;
    case 'in': {
      const def = schema[cond.attr];
      if (!def) throw new Error(`unknown attribute: ${cond.attr}`);
      if (def.kind !== 'enum')
        throw new Error(`attribute ${cond.attr} is not a finite set`);
      for (const v of cond.values) {
        if (!def.values.includes(v))
          throw new Error(`value ${v} not in domain of ${cond.attr}`);
      }
      return;
    }
    case 'cmp': {
      const def = schema[cond.attr];
      if (!def) throw new Error(`unknown attribute: ${cond.attr}`);
      if (def.kind !== 'int')
        throw new Error(`attribute ${cond.attr} is not an integer`);
      if (!Number.isInteger(cond.value))
        throw new Error(`comparison value for ${cond.attr} must be an integer`);
      return;
    }
    case 'and':
    case 'or':
      for (const arg of cond.args) validateCond(arg, schema);
      return;
    case 'not':
      validateCond(cond.arg, schema);
      return;
  }
}

function atomCubes(cond: Cond & { op: 'in' | 'cmp' }, schema: Schema): Cube[] {
  if (cond.op === 'in') {
    const cube = emptyCube();
    cube.sets.set(cond.attr, new Set(cond.values));
    return [cube];
  }
  const def = schema[cond.attr];
  if (def.kind !== 'int') throw new Error(`attribute ${cond.attr} is not an integer`);
  const dom: Interval = { lo: def.min, hi: def.max };
  const v = cond.value;
  // Open/closed boundaries are resolved here into closed integer intervals.
  let intervals: Interval[];
  switch (cond.cmp) {
    case '<': intervals = [{ lo: dom.lo, hi: v - 1 }]; break;
    case '<=': intervals = [{ lo: dom.lo, hi: v }]; break;
    case '==': intervals = [{ lo: v, hi: v }]; break;
    case '>=': intervals = [{ lo: v, hi: dom.hi }]; break;
    case '>': intervals = [{ lo: v + 1, hi: dom.hi }]; break;
    case '!=': intervals = complementInterval({ lo: v, hi: v }, dom); break;
  }
  const cubes: Cube[] = [];
  for (const i of intervals) {
    if (i.lo > i.hi) continue;
    const cube = emptyCube();
    cube.ints.set(cond.attr, i);
    cubes.push(cube);
  }
  return cubes;
}

function crossIntersect(a: Cube[], b: Cube[]): Cube[] {
  const out: Cube[] = [];
  for (const ca of a) {
    for (const cb of b) {
      const inter = intersectCubes(ca, cb);
      if (inter) out.push(inter);
    }
  }
  return out;
}

/** Complement of a single cube: union over its constrained attributes. */
function complementCube(cube: Cube, schema: Schema, order: readonly string[]): Cube[] {
  const out: Cube[] = [];
  for (const attr of order) {
    const def = schema[attr];
    if (def.kind === 'int') {
      const i = cube.ints.get(attr);
      if (!i) continue;
      for (const piece of complementInterval(i, { lo: def.min, hi: def.max })) {
        const c = emptyCube();
        c.ints.set(attr, piece);
        out.push(c);
      }
    } else {
      const s = cube.sets.get(attr);
      if (!s) continue;
      const diff = new Set(def.values.filter((v) => !s.has(v)));
      if (diff.size > 0) {
        const c = emptyCube();
        c.sets.set(attr, diff);
        out.push(c);
      }
    }
  }
  return out;
}

function compute(cond: Cond, schema: Schema, order: readonly string[], cache: DnfCache): Cube[] {
  switch (cond.op) {
    case 'const':
      return cond.value ? [emptyCube()] : [];
    case 'in':
    case 'cmp':
      return atomCubes(cond, schema);
    case 'and': {
      let acc: Cube[] = [emptyCube()];
      for (const arg of cond.args) {
        acc = crossIntersect(acc, dnf(arg, schema, order, cache));
        if (acc.length === 0) break;
      }
      return acc;
    }
    case 'or':
      return cond.args.flatMap((arg) => dnf(arg, schema, order, cache));
    case 'not': {
      // not(DNF) = AND of per-cube complements, then distribute into DNF.
      let acc: Cube[] = [emptyCube()];
      for (const cube of dnf(cond.arg, schema, order, cache)) {
        acc = crossIntersect(acc, complementCube(cube, schema, order));
        if (acc.length === 0) break;
      }
      return acc;
    }
  }
}

export function dnf(
  cond: Cond,
  schema: Schema,
  order: readonly string[],
  cache: DnfCache,
): Cube[] {
  const key = condKey(cond);
  const hit = cache.cache.get(key);
  if (hit) {
    cache.hits++;
    return hit;
  }
  cache.misses++;
  const result = compute(cond, schema, order, cache);
  cache.cache.set(key, result);
  return result;
}
