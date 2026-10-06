import test from 'node:test';
import assert from 'node:assert/strict';
import { and, eq, field, param, render, select } from '../src/index.ts';

test('equal values in distinct param nodes are not merged', () => {
  const q = select(field('id'))
    .from('users')
    .where(and(eq(field('a'), param(1)), eq(field('b'), param(1))));
  const { sql, params } = render(q);
  assert.equal(sql, 'SELECT "id" FROM "users" WHERE ("a" = $1 AND "b" = $2)');
  assert.deepEqual(params, [1, 1]);
});

test('same param node reused gets a single placeholder number', () => {
  const shared = param(7);
  const q = select(field('id'))
    .from('t')
    .where(and(eq(field('a'), shared), eq(field('b'), shared)))
    .limit(shared);
  const { sql, params } = render(q);
  assert.equal(sql, 'SELECT "id" FROM "t" WHERE ("a" = $1 AND "b" = $1) LIMIT $1');
  assert.deepEqual(params, [7]);
});

test('param node shared between IN list and comparison keeps one number', () => {
  const shared = param('x');
  const q = select(field('id'))
    .from('t')
    .where(and(eq(field('a'), shared), eq(field('b'), shared)));
  const first = render(q);
  const second = render(q);
  assert.equal(first.sql, second.sql);
  assert.deepEqual(first.params, second.params);
});
