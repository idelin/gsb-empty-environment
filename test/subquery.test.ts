import test from 'node:test';
import assert from 'node:assert/strict';
import { and, eq, field, inSubquery, param, render, select } from '../src/index.ts';

test('nested subqueries are numbered in deterministic traversal order', () => {
  const inner = select(field('uid')).from('orders').where(eq(field('total'), param(100)));
  const q = select(field('id'))
    .from('users')
    .where(
      and(
        eq(field('status'), param('active')),
        inSubquery(field('id'), inner),
        eq(field('age'), param(3)),
      ),
    );
  const { sql, params } = render(q);
  assert.equal(
    sql,
    'SELECT "id" FROM "users" WHERE ("status" = $1 AND "id" IN ' +
      '(SELECT "uid" FROM "orders" WHERE "total" = $2) AND "age" = $3)',
  );
  assert.deepEqual(params, ['active', 100, 3]);
});

test('FROM subquery params are numbered before WHERE params', () => {
  const sub = select(field('id')).from('a').where(eq(field('x'), param('sub')));
  const q = select(field('id')).fromSubquery(sub, 's').where(eq(field('y'), param('outer')));
  const { sql, params } = render(q);
  assert.equal(
    sql,
    'SELECT "id" FROM (SELECT "id" FROM "a" WHERE "x" = $1) AS "s" WHERE "y" = $2',
  );
  assert.deepEqual(params, ['sub', 'outer']);
});

test('deeply nested subqueries follow depth-first encounter order', () => {
  const deepest = select(field('v')).from('c').where(eq(field('p'), param('deep')));
  const mid = select(field('v')).from('b').where(inSubquery(field('w'), deepest));
  const top = select(field('v')).from('a').where(inSubquery(field('x'), mid));
  const { sql, params } = render(top);
  assert.equal(
    sql,
    'SELECT "v" FROM "a" WHERE "x" IN (SELECT "v" FROM "b" WHERE "w" IN ' +
      '(SELECT "v" FROM "c" WHERE "p" = $1))',
  );
  assert.deepEqual(params, ['deep']);
});
