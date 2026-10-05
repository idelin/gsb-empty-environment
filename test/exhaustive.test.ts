/**
 * Exhaustive cross-validation of the symbolic checker against the independent
 * interpreter at small bit widths.
 *
 * For every pair of expressions from a fixed pool and every width in
 * {1, 2, 3}, the checker's verdict must match brute-force enumeration of all
 * inputs, and a reported counterexample must be *the* lexicographically
 * smallest one. This is exhaustive verification, not random sampling.
 */

import { describe, it } from 'node:test';
import { strict as assert } from 'node:assert';

import {
  add, and, ashr, bin, c, checkEquivalence, ite, lshr, not, or, shl, sub, v, xor,
  type BinaryOp, type Expr,
} from '../src/index.ts';
import { bruteForceCounterexample, structuralKey } from './helpers.ts';

const showCex = (cex: Record<string, bigint>) =>
  JSON.stringify(cex, (_key, value) => (typeof value === 'bigint' ? `${value}n` : value));

function expressionPool(): Expr[] {
  const atoms: Expr[] = [v('x'), v('y'), c(0), c(1)];
  const binops: BinaryOp[] = ['and', 'or', 'xor', 'add', 'sub', 'shl', 'lshr', 'ashr'];
  const pool: Expr[] = [...atoms];
  for (const a of atoms) pool.push(not(a));
  for (const a of atoms) {
    for (const b of atoms) {
      for (const op of binops) pool.push(bin(op, a, b));
    }
  }
  const iteBranches: Array<[Expr, Expr]> = [
    [v('x'), v('y')], [v('y'), v('x')], [c(0), c(1)],
    [c(1), c(0)], [v('x'), c(0)], [c(0), v('x')],
  ];
  for (const cond of atoms) {
    for (const [t, e] of iteBranches) pool.push(ite(cond, t, e));
  }
  pool.push(
    add(not(v('x')), c(1)), // -x
    sub(v('x'), not(v('y'))),
    shl(v('x'), c(1)),
    ashr(v('x'), c(1)),
    ite(add(v('x'), v('y')), v('x'), v('y')),
    and(or(v('x'), v('y')), not(v('x'))),
    xor(xor(v('x'), v('y')), v('x')),
  );
  const seen = new Set<string>();
  return pool.filter((e) => {
    const key = structuralKey(e);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

describe('exhaustive small-width validation', () => {
  const pool = expressionPool();
  const widths = [1, 2, 3, 4];

  for (const width of widths) {
    it(`matches brute force for ${pool.length}^2 expression pairs at width ${width}`, () => {
      let equivalent = 0;
      let different = 0;
      for (let i = 0; i < pool.length; i++) {
        for (let j = i; j < pool.length; j++) {
          const lhs = pool[i];
          const rhs = pool[j];
          const expected = bruteForceCounterexample(lhs, rhs, width);
          const result = checkEquivalence(lhs, rhs, { width });
          if (expected === null) {
            equivalent++;
            assert.equal(
              result.status,
              'equivalent',
              `width ${width}: expected equivalent but got ${JSON.stringify(result)}`,
            );
          } else {
            different++;
            assert.equal(
              result.status,
              'not-equivalent',
              `width ${width}: expected counterexample ${showCex(expected)}`,
            );
            if (result.status === 'not-equivalent') {
              assert.deepEqual(
                result.counterexample,
                expected,
                `width ${width}: counterexample is not the lexicographically smallest`,
              );
            }
          }
        }
      }
      assert.ok(equivalent > 0 && different > 0, 'pool must exercise both outcomes');
    });
  }
});
