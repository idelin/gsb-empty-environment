import test from 'node:test';
import assert from 'node:assert/strict';
import { eq, field, param, raw, render, select } from '../src/index.ts';

test('raw fragments cannot carry value placeholders', () => {
  assert.throws(() => raw('x = ?'), /cannot contain value placeholders/);
  assert.throws(() => raw('x = $1'), /cannot contain value placeholders/);
  assert.throws(() => raw(''), /non-empty/);
});

test('raw fragments render verbatim and bind no values', () => {
  const q = select(raw('COUNT(*)')).from('t').where(eq(field('a'), param(1)));
  const { sql, params } = render(q);
  assert.equal(sql, 'SELECT COUNT(*) FROM "t" WHERE "a" = $1');
  assert.deepEqual(params, [1]);
});
