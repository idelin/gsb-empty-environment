// Seeded random AST generator. To make pure-subtree CSE meaningful, previously
// generated subtrees are occasionally cloned back into the tree (deep copy, no
// aliasing), so identical pure subtrees appear at multiple program points,
// including on different lazy branches.

import type { Expr } from './ast.ts';
import { cloneExpr } from './ast.ts';
import { FUNCTIONS } from './runtime.ts';

export function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const PURE_NAMES = Object.keys(FUNCTIONS).filter((n) => FUNCTIONS[n].pure);
const IMPURE_NAMES = Object.keys(FUNCTIONS).filter((n) => !FUNCTIONS[n].pure);
// Impure functions are oversampled so side effects show up often.
const PICK_LIST = [...PURE_NAMES, ...IMPURE_NAMES, ...IMPURE_NAMES, ...IMPURE_NAMES];

export function randomExpr(seed: number, maxDepth = 6): Expr {
  const rng = mulberry32(seed);
  const pool: Expr[] = [];

  const gen = (depth: number): Expr => {
    if (depth > 0 && pool.length > 0 && rng() < 0.15) {
      return cloneExpr(pool[Math.floor(rng() * pool.length)]);
    }
    let node: Expr;
    const r = rng();
    if (depth <= 0 || r < 0.22) {
      node = { kind: 'lit', value: Math.floor(rng() * 11) - 4 }; // -4..6
    } else if (r < 0.36) {
      node = { kind: 'if', cond: gen(depth - 1), then: gen(depth - 1), else: gen(depth - 1) };
    } else {
      const fn = PICK_LIST[Math.floor(rng() * PICK_LIST.length)];
      const def = FUNCTIONS[fn];
      node = { kind: 'call', fn, args: Array.from({ length: def.arity }, () => gen(depth - 1)) };
    }
    if (pool.length < 300) pool.push(node);
    return node;
  };

  return gen(maxDepth);
}
