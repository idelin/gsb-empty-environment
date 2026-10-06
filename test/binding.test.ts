import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  and,
  eq,
  field,
  ident,
  inList,
  param,
  render,
  select,
  verifyRoundTrip,
} from '../src/index.ts';

test('same value in different param nodes is not merged', () => {
  const a = param('x');
  const b = param('x');
  const q = select({
    columns: [field('id')],
    from: [ident('t')],
    where: and(eq(field('a'), a), eq(field('b'), b)),
  });
  const r = render(q);
  assert.equal(r.sql, 'SELECT "id" FROM "t" WHERE (("a" = $1) AND ("b" = $2))');
  assert.deepEqual(r.bindings, ['x', 'x']);
  assert.equal(r.placeholderIndex(a), 1);
  assert.equal(r.placeholderIndex(b), 2);
});

test('same param node referenced twice gets a single placeholder', () => {
  const a = param('x');
  const q = select({
    columns: [field('id')],
    from: [ident('t')],
    where: and(eq(field('a'), a), eq(field('b'), a)),
  });
  const r = render(q);
  assert.equal(r.sql, 'SELECT "id" FROM "t" WHERE (("a" = $1) AND ("b" = $1))');
  assert.deepEqual(r.bindings, ['x']);
  assert.deepEqual(r.emissionOrder, [1, 1]);
});

test('same param node repeated inside one IN list', () => {
  const one = param(1);
  const two = param(2);
  const q = select({
    columns: [field('id')],
    from: [ident('t')],
    where: inList(field('id'), [one, two, one]),
  });
  const r = render(q);
  assert.equal(r.sql, 'SELECT "id" FROM "t" WHERE ("id" IN ($1, $2, $1))');
  assert.deepEqual(r.bindings, [1, 2]);
});

test('values are never interpolated into the SQL text', () => {
  const hostile = param("x'); DROP TABLE users; --");
  const q = select({
    columns: [field('id')],
    from: [ident('users')],
    where: eq(field('name'), hostile),
  });
  const r = render(q);
  assert.equal(r.sql, 'SELECT "id" FROM "users" WHERE ("name" = $1)');
  assert.ok(!r.sql.includes(hostile.value));
  assert.deepEqual(r.bindings, [hostile.value]);
});

test('render-then-reparse confirms binding correspondence', () => {
  const shared = param('shared');
  const q = select({
    columns: [field('t.id')],
    from: [ident('t')],
    where: and(
      eq(field('t.a'), param(1)),
      eq(field('t.b'), shared),
      eq(field('t.c'), param(1)),
      eq(field('t.d'), shared),
    ),
  });
  const v = verifyRoundTrip(q);
  assert.deepEqual(v.problems, []);
  assert.ok(v.ok);
  assert.deepEqual(v.bindings, [1, 'shared', 1]);
  assert.equal(
    v.sql,
    'SELECT "t"."id" FROM "t" WHERE (("t"."a" = $1) AND ("t"."b" = $2) AND ("t"."c" = $3) AND ("t"."d" = $2))',
  );
});
