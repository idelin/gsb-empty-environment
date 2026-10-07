// Random program generator. A per-program pool of previously built subtrees
// is sampled frequently, so generated programs are rich in duplicated pure
// subexpressions (CSE opportunities), including inside lazy branches and
// around effects.

import { Expr, PureOp, PURE_ARITY, lit, pure, tick, ifE, and, or } from './ast';

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

const PURE_OPS: PureOp[] = [
  'add', 'sub', 'mul',
  'div', 'mod', 'div', 'mod', // extra weight on fallible ops
  'neg', 'abs', 'min', 'max',
];

export function genProgram(rng: () => number): Expr {
  const pool: Expr[] = [];

  const pick = <T>(xs: T[]): T => xs[Math.floor(rng() * xs.length)];

  const leaf = (): Expr => {
    if (pool.length > 0 && rng() < 0.4) return pick(pool);
    return lit(Math.floor(rng() * 5) - 1); // -1..3, zeros are common
  };

  const gen = (depth: number): Expr => {
    if (depth <= 0) return leaf();
    if (pool.length > 0 && rng() < 0.35) return pick(pool);

    let e: Expr;
    const k = rng();
    if (k < 0.4) {
      const op = pick(PURE_OPS);
      const args = Array.from({ length: PURE_ARITY[op] }, () => gen(depth - 1));
      if ((op === 'div' || op === 'mod') && rng() < 0.3) args[1] = lit(0);
      e = pure(op, ...args);
    } else if (k < 0.52) {
      e = tick(gen(depth - 1));
    } else if (k < 0.68) {
      e = ifE(gen(depth - 1), gen(depth - 1), gen(depth - 1));
    } else if (k < 0.84) {
      e = and(gen(depth - 1), gen(depth - 1));
    } else {
      e = or(gen(depth - 1), gen(depth - 1));
    }

    if (pool.length < 12 && rng() < 0.3) pool.push(e);
    return e;
  };

  return gen(4 + Math.floor(rng() * 3));
}
