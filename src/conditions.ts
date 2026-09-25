// Condition AST with hash-consing: structurally identical conditions share a
// single node (shared decision representation). Policies may only read
// explicit attributes via finite-set predicates, integer conditions and
// boolean combinations. No external identity services are consulted.

export type CmpOp = '<' | '<=' | '==' | '!=' | '>=' | '>';
export type DiffOp = '<' | '<=' | '==' | '>=' | '>';
export type Value = number | string;

export type Cond =
  | { readonly id: number; readonly kind: 'const'; readonly value: boolean }
  | { readonly id: number; readonly kind: 'cmp'; readonly var: string; readonly op: CmpOp; readonly value: number }
  | { readonly id: number; readonly kind: 'diff'; readonly left: string; readonly right: string; readonly op: DiffOp; readonly c: number }
  | { readonly id: number; readonly kind: 'in'; readonly var: string; readonly values: readonly Value[] }
  | { readonly id: number; readonly kind: 'not'; readonly a: Cond }
  | { readonly id: number; readonly kind: 'and'; readonly args: readonly Cond[] }
  | { readonly id: number; readonly kind: 'or'; readonly args: readonly Cond[] };

// ---------------------------------------------------------------- hash-consing

const intern = new Map<string, Cond>();
let nextId = 0;

function node<T extends Cond>(key: string, make: (id: number) => T): T {
  const hit = intern.get(key);
  if (hit !== undefined) return hit as T;
  const made = make(nextId++);
  intern.set(key, made);
  return made;
}

function constNode(value: boolean): Cond {
  return node(`const:${value}`, (id) => ({ id, kind: 'const', value }));
}

export const TRUE: Cond = constNode(true);
export const FALSE: Cond = constNode(false);

export function cmp(variable: string, op: CmpOp, value: number): Cond {
  return node(`cmp:${variable}:${op}:${value}`, (id) => ({ id, kind: 'cmp', var: variable, op, value }));
}

/** Difference constraint: left - right <op> c. */
export function diff(left: string, op: DiffOp, right: string, c: number): Cond {
  return node(`diff:${left}:${op}:${right}:${c}`, (id) => ({ id, kind: 'diff', left, right, op, c }));
}

function sortValues(values: readonly Value[]): Value[] {
  const uniq = [...new Set(values)];
  return uniq.sort((a, b) => {
    if (typeof a === 'number' && typeof b === 'number') return a - b;
    if (typeof a === 'number') return -1;
    if (typeof b === 'number') return 1;
    return a < b ? -1 : a > b ? 1 : 0;
  });
}

export function inSet(variable: string, values: readonly Value[]): Cond {
  const sorted = sortValues(values);
  return node(`in:${variable}:${JSON.stringify(sorted)}`, (id) => ({ id, kind: 'in', var: variable, values: sorted }));
}

export function not(a: Cond): Cond {
  if (a.kind === 'const') return constNode(!a.value);
  if (a.kind === 'not') return a.a;
  return node(`not:${a.id}`, (id) => ({ id, kind: 'not', a }));
}

function boolNode(kind: 'and' | 'or', args: readonly Cond[]): Cond {
  const flat: Cond[] = [];
  const seen = new Set<number>();
  for (const arg of args) {
    if (arg.kind === kind) {
      for (const inner of arg.args) {
        if (!seen.has(inner.id)) { seen.add(inner.id); flat.push(inner); }
      }
    } else if (!seen.has(arg.id)) {
      seen.add(arg.id);
      flat.push(arg);
    }
  }
  const identity = kind === 'and';
  if (flat.some((a) => a.kind === 'const' && a.value !== identity)) return constNode(!identity);
  const kept = flat.filter((a) => a.kind !== 'const');
  if (kept.length === 0) return constNode(identity);
  if (kept.length === 1) return kept[0];
  kept.sort((a, b) => a.id - b.id);
  return node(`${kind}:${kept.map((a) => a.id).join(',')}`, (id) =>
    kind === 'and' ? { id, kind: 'and', args: kept } : { id, kind: 'or', args: kept });
}

export function and(...args: readonly Cond[]): Cond { return boolNode('and', args); }
export function or(...args: readonly Cond[]): Cond { return boolNode('or', args); }

// Set-algebra sugar over condition scopes.
export const intersect = and;
export const union = or;
export function difference(a: Cond, b: Cond): Cond { return and(a, not(b)); }

// Convenience integer-comparison builders.
export const ge = (v: string, n: number): Cond => cmp(v, '>=', n);
export const gt = (v: string, n: number): Cond => cmp(v, '>', n);
export const le = (v: string, n: number): Cond => cmp(v, '<=', n);
export const lt = (v: string, n: number): Cond => cmp(v, '<', n);
export const eq = (v: string, n: number): Cond => cmp(v, '==', n);
export const ne = (v: string, n: number): Cond => cmp(v, '!=', n);
export const between = (v: string, lo: number, hi: number): Cond => and(ge(v, lo), le(v, hi));

// ---------------------------------------------------------------- variables

export function collectVars(cond: Cond, into: Set<string> = new Set()): Set<string> {
  switch (cond.kind) {
    case 'const': break;
    case 'cmp': case 'in': into.add(cond.var); break;
    case 'diff': into.add(cond.left); into.add(cond.right); break;
    case 'not': collectVars(cond.a, into); break;
    case 'and': case 'or': for (const a of cond.args) collectVars(a, into); break;
  }
  return into;
}

/** Integer constants that appear in cmp predicates, per variable (split hints). */
export function boundaryConstants(conds: readonly Cond[]): Map<string, number[]> {
  const out = new Map<string, Set<number>>();
  const visit = (c: Cond): void => {
    switch (c.kind) {
      case 'cmp': {
        let s = out.get(c.var);
        if (!s) { s = new Set(); out.set(c.var, s); }
        s.add(c.value);
        break;
      }
      case 'not': visit(c.a); break;
      case 'and': case 'or': for (const a of c.args) visit(a); break;
      default: break;
    }
  };
  for (const c of conds) visit(c);
  return new Map([...out].map(([k, s]) => [k, [...s].sort((a, b) => a - b)]));
}

// ---------------------------------------------------------------- three-valued point evaluation

export type Tri = 'T' | 'F' | 'U';

export type MaybeValue = Value | null; // null = unknown attribute (⊥)
export type Point = Readonly<Record<string, MaybeValue>>;

const negateTri = (t: Tri): Tri => (t === 'T' ? 'F' : t === 'F' ? 'T' : 'U');
const andTri = (a: Tri, b: Tri): Tri => (a === 'F' || b === 'F' ? 'F' : a === 'U' || b === 'U' ? 'U' : 'T');
const orTri = (a: Tri, b: Tri): Tri => (a === 'T' || b === 'T' ? 'T' : a === 'U' || b === 'U' ? 'U' : 'F');

function compareNumber(x: number, op: CmpOp | DiffOp, c: number): boolean {
  switch (op) {
    case '<': return x < c;
    case '<=': return x <= c;
    case '==': return x === c;
    case '!=': return x !== c;
    case '>=': return x >= c;
    case '>': return x > c;
  }
}

/** Kleene strong three-valued evaluation on a concrete (possibly partial) point. */
export function evalPoint(cond: Cond, point: Point): Tri {
  switch (cond.kind) {
    case 'const': return cond.value ? 'T' : 'F';
    case 'cmp': {
      const v = point[cond.var];
      if (v === null || v === undefined || typeof v !== 'number') return 'U';
      return compareNumber(v, cond.op, cond.value) ? 'T' : 'F';
    }
    case 'diff': {
      const l = point[cond.left];
      const r = point[cond.right];
      if (l === null || l === undefined || r === null || r === undefined) return 'U';
      if (typeof l !== 'number' || typeof r !== 'number') return 'U';
      return compareNumber(l - r, cond.op, cond.c) ? 'T' : 'F';
    }
    case 'in': {
      const v = point[cond.var];
      if (v === null || v === undefined) return 'U';
      return cond.values.includes(v) ? 'T' : 'F';
    }
    case 'not': return negateTri(evalPoint(cond.a, point));
    case 'and': {
      let acc: Tri = 'T';
      for (const a of cond.args) { acc = andTri(acc, evalPoint(a, point)); if (acc === 'F') break; }
      return acc;
    }
    case 'or': {
      let acc: Tri = 'F';
      for (const a of cond.args) { acc = orTri(acc, evalPoint(a, point)); if (acc === 'T') break; }
      return acc;
    }
  }
}

// ---------------------------------------------------------------- pretty printing

export function formatCond(cond: Cond): string {
  switch (cond.kind) {
    case 'const': return cond.value ? 'true' : 'false';
    case 'cmp': return `${cond.var} ${cond.op} ${cond.value}`;
    case 'diff': return `${cond.left} - ${cond.right} ${cond.op} ${cond.c}`;
    case 'in': return `${cond.var} in {${cond.values.join(', ')}}`;
    case 'not': return `not (${formatCond(cond.a)})`;
    case 'and': return `(${cond.args.map(formatCond).join(' and ')})`;
    case 'or': return `(${cond.args.map(formatCond).join(' or ')})`;
  }
}
