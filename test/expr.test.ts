import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  evalExpr,
  UnknownAttributeError,
  NonIntegerAttributeError,
  type Attrs,
} from '../src/expr.ts';

const attrs: Attrs = Object.freeze({
  role: 'analyst',
  amount: 4200,
  region: 'eu',
  vip: false,
});

test('finite-set membership', () => {
  assert.equal(evalExpr({ op: 'in', attr: 'role', values: ['analyst', 'auditor'] }, attrs), true);
  assert.equal(evalExpr({ op: 'in', attr: 'role', values: ['admin'] }, attrs), false);
  assert.equal(evalExpr({ op: 'in', attr: 'region', values: ['us', 'eu'] }, attrs), true);
});

test('integer conditions', () => {
  assert.equal(evalExpr({ op: 'intCmp', attr: 'amount', cmp: '>=', value: 1000 }, attrs), true);
  assert.equal(evalExpr({ op: 'intCmp', attr: 'amount', cmp: '<', value: 100 }, attrs), false);
  assert.equal(evalExpr({ op: 'intCmp', attr: 'amount', cmp: '==', value: 4200 }, attrs), true);
});

test('boolean combinators', () => {
  const expr = {
    op: 'and',
    args: [
      { op: 'in', attr: 'role', values: ['analyst'] },
      { op: 'or', args: [
        { op: 'intCmp', attr: 'amount', cmp: '>', value: 1000 },
        { op: 'eq', attr: 'vip', value: true },
      ] },
      { op: 'not', arg: { op: 'eq', attr: 'region', value: 'us' } },
    ],
  } as const;
  assert.equal(evalExpr(expr, attrs), true);
});

test('unknown attribute access fails explicitly', () => {
  assert.throws(
    () => evalExpr({ op: 'eq', attr: 'department', value: 'it' }, attrs),
    UnknownAttributeError,
  );
});

test('intCmp requires integers', () => {
  assert.throws(
    () => evalExpr({ op: 'intCmp', attr: 'role', cmp: '>', value: 1 }, attrs),
    NonIntegerAttributeError,
  );
  assert.throws(
    () => evalExpr({ op: 'intCmp', attr: 'amount', cmp: '>', value: 1.5 }, attrs),
    NonIntegerAttributeError,
  );
});

test('attributes are read-only: frozen attrs evaluate fine and are not mutated', () => {
  const frozen = Object.freeze({ n: 7 });
  assert.equal(evalExpr({ op: 'intCmp', attr: 'n', cmp: '>=', value: 7 }, frozen), true);
  assert.deepEqual(frozen, { n: 7 });
});
