import test from 'node:test';
import assert from 'node:assert/strict';
import {
  EvalError,
  Expr,
  Row,
  Tri,
  and,
  cmp,
  evaluate,
  field,
  filterRows,
  isNotNull,
  isNull,
  lit,
  not,
  or,
  triAnd,
  triNot,
  triOr,
} from '../src/index';

// Constant-valued expressions: T, F, and U (via a missing field).
const T: Expr = cmp('eq', lit(1), lit(1));
const F: Expr = cmp('eq', lit(1), lit(2));
const U: Expr = cmp('eq', field('missing'), lit(1));
const triExprs: Record<Tri, Expr> = { T, F, U };

test('AND/OR/NOT match the Kleene truth tables', () => {
  const tris: Tri[] = ['T', 'F', 'U'];
  for (const a of tris) {
    for (const b of tris) {
      assert.equal(evaluate(and(triExprs[a], triExprs[b]), {}), triAnd(a, b), `${a} AND ${b}`);
      assert.equal(evaluate(or(triExprs[a], triExprs[b]), {}), triOr(a, b), `${a} OR ${b}`);
    }
    assert.equal(evaluate(not(triExprs[a]), {}), triNot(a), `NOT ${a}`);
  }
});

test('IS NULL treats missing fields and null alike', () => {
  const rows: Row[] = [
    {}, // missing
    { a: null },
    { a: 0 },
    { a: '' },
    { a: false },
  ];
  assert.deepEqual(filterRows(rows, isNull(field('a'))), [{}, { a: null }]);
  assert.deepEqual(filterRows(rows, isNotNull(field('a'))), [{ a: 0 }, { a: '' }, { a: false }]);
});

test('missing fields and nulls make comparisons unknown, never true', () => {
  const rows: Row[] = [{}, { a: null }, { a: 1 }, { a: 2 }];
  const pred = cmp('ge', field('a'), lit(1));
  assert.equal(evaluate(pred, rows[0]), 'U');
  assert.equal(evaluate(pred, rows[1]), 'U');
  // Only true results are selected; unknown and false are dropped.
  assert.deepEqual(filterRows(rows, pred), [{ a: 1 }, { a: 2 }]);
  assert.deepEqual(filterRows(rows, cmp('ne', field('a'), lit(1))), [{ a: 2 }]);
});

test('comparisons evaluate across supported types', () => {
  assert.equal(evaluate(cmp('lt', lit(1), lit(2)), {}), 'T');
  assert.equal(evaluate(cmp('lt', lit('a'), lit('b')), {}), 'T');
  assert.equal(evaluate(cmp('eq', lit(true), lit(true)), {}), 'T');
  assert.equal(evaluate(cmp('ne', lit(false), lit(true)), {}), 'T');
});

test('type errors are reported with the position of the comparison', () => {
  assert.throws(
    () => evaluate(cmp('eq', lit(1), lit('a')), {}),
    (err: unknown) => err instanceof EvalError && err.path.length === 0,
  );
  assert.throws(
    () => evaluate(cmp('gt', lit(true), lit(false)), {}),
    (err: unknown) => err instanceof EvalError && /order booleans/.test(err.message),
  );
  const nested = and(T, or(F, cmp('eq', lit(1), lit('a'))));
  assert.throws(
    () => evaluate(nested, {}),
    (err: unknown) => {
      assert.ok(err instanceof EvalError);
      assert.deepEqual(err.path, ['right', 'right']);
      return true;
    },
  );
});

test('short-circuited branches are never evaluated', () => {
  // A field whose getter throws proves the branch was not touched.
  const trapRow: Row = {};
  Object.defineProperty(trapRow, 'trap', {
    get() {
      throw new Error('evaluated a short-circuited branch');
    },
  });
  const trap = cmp('eq', field('trap'), lit(1));
  assert.equal(evaluate(and(F, trap), trapRow), 'F');
  assert.equal(evaluate(or(T, trap), trapRow), 'T');
  // A type error in a skipped branch stays silent.
  const bad = cmp('eq', lit(1), lit('a'));
  assert.equal(evaluate(and(F, bad), {}), 'F');
  assert.equal(evaluate(or(T, bad), {}), 'T');
});

test('error priority: evaluated branches raise, in left-to-right order', () => {
  const badLeft = cmp('eq', lit(1), lit('a'));
  const badRight = cmp('eq', lit(2), lit('b'));
  // Left branch is reached and fails before the right is considered.
  assert.throws(
    () => evaluate(and(badLeft, badRight), {}),
    (err: unknown) => {
      assert.ok(err instanceof EvalError);
      assert.deepEqual(err.path, ['left']);
      return true;
    },
  );
  // AND on true / OR on false must evaluate the right branch, so it raises.
  assert.throws(
    () => evaluate(and(T, badRight), {}),
    (err: unknown) => {
      assert.ok(err instanceof EvalError);
      assert.deepEqual(err.path, ['right']);
      return true;
    },
  );
  assert.throws(
    () => evaluate(or(F, badRight), {}),
    (err: unknown) => {
      assert.ok(err instanceof EvalError);
      assert.deepEqual(err.path, ['right']);
      return true;
    },
  );
  // Unknown does not short-circuit: AND(U, bad) and OR(U, bad) both raise.
  assert.throws(() => evaluate(and(U, badRight), {}), EvalError);
  assert.throws(() => evaluate(or(U, badRight), {}), EvalError);
});

test('filterRows composes predicates with three-valued logic', () => {
  const rows: Row[] = [
    { a: 1, b: 'x' },
    { a: 2, b: 'x' },
    { a: 2, b: 'y' },
    { a: 3 },
    { b: 'x' },
  ];
  const pred = and(cmp('gt', field('a'), lit(1)), or(cmp('eq', field('b'), lit('x')), isNull(field('b'))));
  assert.deepEqual(filterRows(rows, pred), [{ a: 2, b: 'x' }, { a: 3 }]);
});
