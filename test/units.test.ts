import { describe, it } from 'node:test';
import { strict as assert } from 'node:assert';

import {
  add, and, ashr, c, checkEquivalence, evaluate, ite, lshr, not, or, shl, sub, v, xor,
} from '../src/index.ts';

const envOf = (entries: Record<string, bigint>) => new Map(Object.entries(entries));

describe('interpreter semantics', () => {
  it('truncates results to the bit width', () => {
    assert.equal(evaluate(add(c(15), c(1)), envOf({}), 4), 0n); // 15 + 1 wraps
    assert.equal(evaluate(sub(c(0), c(1)), envOf({}), 4), 15n); // 0 - 1 wraps
    assert.equal(evaluate(not(c(0)), envOf({}), 4), 15n);
    assert.equal(evaluate(c(0xff), envOf({}), 4), 15n); // constants truncated
  });

  it('fixes shift-by->=-width to zero', () => {
    const env = envOf({ x: 0xffn });
    for (const width of [1, 2, 4, 8]) {
      const w = BigInt(width);
      assert.equal(evaluate(shl(v('x'), c(w)), env, width), 0n);
      assert.equal(evaluate(lshr(v('x'), c(w)), env, width), 0n);
      assert.equal(evaluate(ashr(v('x'), c(w)), env, width), 0n);
      // A shift amount is itself truncated to the width: at width >= 2 the
      // constant w+5 stays >= width, so the result is still zero.
      if (width >= 2) {
        assert.equal(evaluate(shl(v('x'), c(w + 5n)), env, width), 0n);
      }
    }
  });

  it('computes arithmetic shift with sign extension below the width', () => {
    const env = envOf({ x: 0b1000n });
    assert.equal(evaluate(ashr(v('x'), c(1)), env, 4), 0b1100n);
    assert.equal(evaluate(lshr(v('x'), c(1)), env, 4), 0b0100n);
  });

  it('treats ite condition as nonzero test', () => {
    const env = envOf({ x: 2n, y: 5n });
    assert.equal(evaluate(ite(c(3), v('x'), v('y')), env, 4), 2n);
    assert.equal(evaluate(ite(c(0), v('x'), v('y')), env, 4), 5n);
    // 4 truncates to 0 at width 2, and y = 5 truncates to 1.
    assert.equal(evaluate(ite(c(4), v('x'), v('y')), env, 2), 1n);
  });
});

describe('checker: known identities (proofs, not samples)', () => {
  type ExprOf = Parameters<typeof checkEquivalence>[0];
  const identities: Array<[string, ExprOf, ExprOf]> = [
    ['add commutes', add(v('x'), v('y')), add(v('y'), v('x'))],
    ['xor cancels', xor(xor(v('x'), v('y')), v('x')), v('y')],
    ['sub is add-neg', sub(v('x'), v('y')), add(add(v('x'), not(v('y'))), c(1))],
    ['shl1 is doubling', shl(v('x'), c(1)), add(v('x'), v('x'))],
    ['de morgan', not(and(v('x'), v('y'))), or(not(v('x')), not(v('y')))],
    ['absorption', or(and(v('x'), v('y')), v('x')), v('x')],
    ['ite with equal branches', ite(v('c'), v('x'), v('x')), v('x')],
    ['constant truncation', c(64), c(0)], // 64 = 2^6 vanishes at widths 1..6
  ];
  // Width-dependent identities: shifting by the width itself is always zero.
  const widthIdentities: Array<[string, (width: number) => [ExprOf, ExprOf]]> = [
    ['shift by width is zero (shl)', (w) => [shl(v('x'), c(BigInt(w))), c(0)]],
    ['shift by width is zero (lshr)', (w) => [lshr(v('x'), c(BigInt(w))), c(0)]],
    ['shift by width is zero (ashr)', (w) => [ashr(v('x'), c(BigInt(w))), c(0)]],
  ];
  for (const [name, lhs, rhs] of identities) {
    it(`proves ${name} at widths 1..6`, () => {
      for (let width = 1; width <= 6; width++) {
        const result = checkEquivalence(lhs, rhs, { width });
        assert.equal(result.status, 'equivalent', `width ${width}: ${JSON.stringify(result)}`);
      }
    });
  }
  for (const [name, make] of widthIdentities) {
    it(`proves ${name} at widths 1..6`, () => {
      for (let width = 1; width <= 6; width++) {
        const [lhs, rhs] = make(width);
        const result = checkEquivalence(lhs, rhs, { width });
        assert.equal(result.status, 'equivalent', `width ${width}: ${JSON.stringify(result)}`);
      }
    });
  }
});

describe('checker: minimal counterexamples', () => {
  it('finds x = 0 for x + 1 vs x', () => {
    const result = checkEquivalence(add(v('x'), c(1)), v('x'), { width: 4 });
    assert.deepEqual(result, { status: 'not-equivalent', counterexample: { x: 0n } });
  });

  it('finds x = 1 for x << 1 vs x', () => {
    const result = checkEquivalence(shl(v('x'), c(1)), v('x'), { width: 4 });
    assert.deepEqual(result, { status: 'not-equivalent', counterexample: { x: 1n } });
  });

  it('finds the sign bit for ashr vs lshr', () => {
    const result = checkEquivalence(ashr(v('x'), c(1)), lshr(v('x'), c(1)), { width: 4 });
    assert.deepEqual(result, { status: 'not-equivalent', counterexample: { x: 8n } });
  });

  it('minimizes lexicographically across variables (x before y)', () => {
    // and(x,y) != or(x,y) exactly when x != y; smallest is x=0, y=1.
    const result = checkEquivalence(and(v('x'), v('y')), or(v('x'), v('y')), { width: 2 });
    assert.deepEqual(result, {
      status: 'not-equivalent',
      counterexample: { x: 0n, y: 1n },
    });
  });

  it('distinguishes swapped ite branches', () => {
    // ite(c, x, y) vs ite(c, y, x): with c=0 the first yields y and the
    // second yields x, so the smallest diff is c=0, x=0, y=1.
    const result = checkEquivalence(
      ite(v('c'), v('x'), v('y')),
      ite(v('c'), v('y'), v('x')),
      { width: 1 },
    );
    assert.deepEqual(result, {
      status: 'not-equivalent',
      counterexample: { c: 0n, x: 0n, y: 1n },
    });
  });

  it('handles variable shift amounts (shl(x, y) vs 0 at width 1)', () => {
    const result = checkEquivalence(shl(v('x'), v('y')), c(0), { width: 1 });
    assert.deepEqual(result, {
      status: 'not-equivalent',
      counterexample: { x: 1n, y: 0n },
    });
  });
});

describe('checker: budget', () => {
  it('returns unknown when the budget is exhausted', () => {
    const result = checkEquivalence(add(v('x'), v('y')), add(v('y'), v('x')), {
      width: 8,
      budget: 0,
    });
    assert.equal(result.status, 'unknown');
  });

  it('still proves with a sufficient budget', () => {
    const result = checkEquivalence(add(v('x'), v('y')), add(v('y'), v('x')), {
      width: 8,
      budget: 100_000,
    });
    assert.equal(result.status, 'equivalent');
  });
});
