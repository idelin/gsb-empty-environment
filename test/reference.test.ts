import test from 'node:test';
import assert from 'node:assert/strict';
import {
  EvalError,
  Expr,
  Operand,
  Row,
  Value,
  and,
  cmp,
  evaluate,
  expectedOutcome,
  field,
  isNotNull,
  isNull,
  lit,
  not,
  or,
} from '../src/index';

const CmpOps = ['eq', 'ne', 'lt', 'le', 'gt', 'ge'] as const;

// Operands exercising present/absent fields and every scalar type.
const operands: Operand[] = [
  field('a'),
  field('b'),
  lit(1),
  lit('x'),
  lit(true),
  lit(null),
];

// Row values: missing (absent key), null, and both values of each scalar type.
const cellValues: (Value | undefined)[] = [undefined, null, 1, 2, 'x', 'y', true, false];

const rows: Row[] = [];
for (const a of cellValues) {
  for (const b of cellValues) {
    const row: Row = {};
    if (a !== undefined) row.a = a;
    if (b !== undefined) row.b = b;
    rows.push(row);
  }
}

// Depth-0 expressions: every comparison over the operand grid, plus IS NULL.
const base: Expr[] = [];
for (const op of CmpOps) {
  for (const left of operands) {
    for (const right of operands) {
      base.push(cmp(op, left, right));
    }
  }
}
for (const operand of [field('a'), field('b'), lit(null), lit(1)]) {
  base.push(isNull(operand), isNotNull(operand));
}

// Depth-1 expressions: NOT over everything, AND/OR over a representative
// sample of bases (keeps the cross-product tractable while covering every
// combination of tri-valued outcomes and error/no-error leaves).
const sample = base.filter((_, i) => i % 17 === 0);
const exprs: Expr[] = [...base];
for (const e of base.filter((_, i) => i % 13 === 0)) exprs.push(not(e));
for (const l of sample) {
  for (const r of sample) {
    exprs.push(and(l, r), or(l, r));
  }
}

test('short-circuit evaluator matches the truth-table interpreter', () => {
  let checked = 0;
  let errorsSeen = 0;
  for (const expr of exprs) {
    for (const row of rows) {
      const expected = expectedOutcome(expr, row);
      if (expected.errors.length > 0) {
        errorsSeen += 1;
        // The first reachable type error must be thrown, at the same position.
        assert.throws(
          () => evaluate(expr, row),
          (err: unknown) => {
            assert.ok(err instanceof EvalError);
            assert.deepEqual([...err.path], [...expected.errors[0].path]);
            return true;
          },
        );
      } else {
        assert.equal(evaluate(expr, row), expected.value);
      }
      checked += 1;
    }
  }
  // Sanity: the enumeration really exercised both values and type errors.
  assert.ok(checked > 10000, `expected a large cross-product, got ${checked}`);
  assert.ok(errorsSeen > 0, 'expected some type errors in the enumeration');
});

test('truth-table interpreter reproduces the Kleene tables on all tri combos', () => {
  const triExpr: Record<'T' | 'F' | 'U', Expr> = {
    T: cmp('eq', lit(1), lit(1)),
    F: cmp('eq', lit(1), lit(2)),
    U: cmp('eq', field('missing'), lit(1)),
  };
  const tris = ['T', 'F', 'U'] as const;
  for (const a of tris) {
    for (const b of tris) {
      assert.equal(expectedOutcome(and(triExpr[a], triExpr[b]), {}).value, evaluate(and(triExpr[a], triExpr[b]), {}));
      assert.equal(expectedOutcome(or(triExpr[a], triExpr[b]), {}).value, evaluate(or(triExpr[a], triExpr[b]), {}));
    }
  }
});
