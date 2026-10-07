// Expression AST.
//
// Source language (generated programs): lit / pure / eff / if / and / or.
// Optimized output may additionally contain let / var (introduced by CSE).

export type PureOp =
  | 'add' | 'sub' | 'mul' | 'div' | 'mod'
  | 'neg' | 'abs' | 'min' | 'max';

export type Expr =
  | { tag: 'lit'; value: number }
  | { tag: 'pure'; op: PureOp; args: Expr[] }          // pure, may throw (div/mod by zero)
  | { tag: 'eff'; op: 'tick'; args: Expr[] }           // counted side effect
  | { tag: 'if'; cond: Expr; then: Expr; els: Expr }   // lazy branches
  | { tag: 'and'; lhs: Expr; rhs: Expr }               // short-circuit (lazy rhs)
  | { tag: 'or'; lhs: Expr; rhs: Expr }                // short-circuit (lazy rhs)
  | { tag: 'let'; name: string; value: Expr; body: Expr }
  | { tag: 'var'; name: string };

export const PURE_ARITY: Record<PureOp, number> = {
  add: 2, sub: 2, mul: 2, div: 2, mod: 2, neg: 1, abs: 1, min: 2, max: 2,
};

export const lit = (value: number): Expr => ({ tag: 'lit', value });
export const pure = (op: PureOp, ...args: Expr[]): Expr => ({ tag: 'pure', op, args });
export const tick = (arg: Expr): Expr => ({ tag: 'eff', op: 'tick', args: [arg] });
export const ifE = (cond: Expr, then: Expr, els: Expr): Expr => ({ tag: 'if', cond, then, els });
export const and = (lhs: Expr, rhs: Expr): Expr => ({ tag: 'and', lhs, rhs });
export const or = (lhs: Expr, rhs: Expr): Expr => ({ tag: 'or', lhs, rhs });

// Children in evaluation order.
export function children(e: Expr): Expr[] {
  switch (e.tag) {
    case 'pure':
    case 'eff':
      return e.args;
    case 'if':
      return [e.cond, e.then, e.els];
    case 'and':
    case 'or':
      return [e.lhs, e.rhs];
    case 'let':
      return [e.value, e.body];
    default:
      return [];
  }
}

// Is child i in a strict position (evaluated unconditionally, in order,
// whenever the parent is evaluated)?
export function isStrictChild(e: Expr, i: number): boolean {
  switch (e.tag) {
    case 'pure':
    case 'eff':
      return true;
    case 'if':
      return i === 0;
    case 'and':
    case 'or':
      return i === 0;
    case 'let':
      return true; // value first, then body
    default:
      return false;
  }
}

export function withChild(e: Expr, i: number, c: Expr): Expr {
  switch (e.tag) {
    case 'pure':
    case 'eff': {
      const args = e.args.slice();
      args[i] = c;
      return { ...e, args };
    }
    case 'if':
      return i === 0 ? { ...e, cond: c } : i === 1 ? { ...e, then: c } : { ...e, els: c };
    case 'and':
    case 'or':
      return i === 0 ? { ...e, lhs: c } : { ...e, rhs: c };
    case 'let':
      return i === 0 ? { ...e, value: c } : { ...e, body: c };
    default:
      throw new Error('withChild: leaf node');
  }
}

// Canonical structural key. Two subtrees with equal hashes are
// semantically interchangeable (same pure value / same error).
// Memoized per node object; Exprs are treated as immutable and the
// optimizer shares untouched subtrees by reference, so this stays valid.
const hashCache = new WeakMap<Expr, string>();

export function hash(e: Expr): string {
  const hit = hashCache.get(e);
  if (hit !== undefined) return hit;
  const h = hashSlow(e);
  hashCache.set(e, h);
  return h;
}

function hashSlow(e: Expr): string {
  switch (e.tag) {
    case 'lit':
      return `lit:${e.value}`;
    case 'pure':
      return `pure:${e.op}(${e.args.map(hash).join(',')})`;
    case 'eff':
      return `eff:${e.op}(${e.args.map(hash).join(',')})`;
    case 'if':
      return `if(${hash(e.cond)},${hash(e.then)},${hash(e.els)})`;
    case 'and':
      return `and(${hash(e.lhs)},${hash(e.rhs)})`;
    case 'or':
      return `or(${hash(e.lhs)},${hash(e.rhs)})`;
    case 'let':
      return `let(${e.name}=${hash(e.value)},${hash(e.body)})`;
    case 'var':
      return `var:${e.name}`;
  }
}

export interface Info {
  hasEff: boolean;       // contains a side-effecting call
  hasFallible: boolean;  // contains div/mod (may throw)
  size: number;
}

const infoCache = new WeakMap<Expr, Info>();

export function analyze(e: Expr): Info {
  const hit = infoCache.get(e);
  if (hit !== undefined) return hit;
  const kids = children(e).map(analyze);
  const info: Info = {
    hasEff: e.tag === 'eff' || kids.some((k) => k.hasEff),
    hasFallible:
      (e.tag === 'pure' && (e.op === 'div' || e.op === 'mod')) ||
      kids.some((k) => k.hasFallible),
    size: 1 + kids.reduce((a, k) => a + k.size, 0),
  };
  infoCache.set(e, info);
  return info;
}

// Pure and total: no effects, cannot throw. Such a subtree may be
// evaluated anywhere (even speculatively) without observable change.
export function isPureTotal(e: Expr): boolean {
  const i = analyze(e);
  return !i.hasEff && !i.hasFallible;
}

export function render(e: Expr): string {
  switch (e.tag) {
    case 'lit':
      return String(e.value);
    case 'pure':
      return `${e.op}(${e.args.map(render).join(', ')})`;
    case 'eff':
      return `${e.op}(${e.args.map(render).join(', ')})`;
    case 'if':
      return `if(${render(e.cond)}, ${render(e.then)}, ${render(e.els)})`;
    case 'and':
      return `and(${render(e.lhs)}, ${render(e.rhs)})`;
    case 'or':
      return `or(${render(e.lhs)}, ${render(e.rhs)})`;
    case 'let':
      return `let ${e.name} = ${render(e.value)} in ${render(e.body)}`;
    case 'var':
      return e.name;
  }
}
