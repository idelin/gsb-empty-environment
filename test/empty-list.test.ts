import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  and,
  eq,
  field,
  ident,
  inList,
  notInList,
  or,
  param,
  render,
  select,
  verifyRoundTrip,
} from '../src/index.ts';

test('empty IN list renders as a constant-false predicate', () => {
  const q = select({
    columns: [field('id')],
    from: [ident('t')],
    where: inList(field('id'), []),
  });
  const r = render(q);
  assert.equal(r.sql, 'SELECT "id" FROM "t" WHERE (1 = 0)');
  assert.deepEqual(r.bindings, []);
  assert.ok(verifyRoundTrip(q).ok);
});

test('empty NOT IN list renders as a constant-true predicate', () => {
  const q = select({
    columns: [field('id')],
    from: [ident('t')],
    where: notInList(field('id'), []),
  });
  const r = render(q);
  assert.equal(r.sql, 'SELECT "id" FROM "t" WHERE (1 = 1)');
  assert.deepEqual(r.bindings, []);
  assert.ok(verifyRoundTrip(q).ok);
});

test('empty AND / OR groups render as constant true / false', () => {
  const q = select({
    columns: [field('id')],
    from: [ident('t')],
    where: and(inList(field('id'), []), and(), or()),
  });
  const r = render(q);
  assert.equal(r.sql, 'SELECT "id" FROM "t" WHERE ((1 = 0) AND (1 = 1) AND (1 = 0))');
  assert.deepEqual(r.bindings, []);
  assert.ok(verifyRoundTrip(q).ok);
});

test('empty list mixed with bound params keeps numbering intact', () => {
  const p = param(5);
  const q = select({
    columns: [field('id')],
    from: [ident('t')],
    where: and(inList(field('id'), []), eq(field('n'), p)),
  });
  const r = render(q);
  assert.equal(r.sql, 'SELECT "id" FROM "t" WHERE ((1 = 0) AND ("n" = $1))');
  assert.deepEqual(r.bindings, [5]);
  assert.ok(verifyRoundTrip(q).ok);
});
