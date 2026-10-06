import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  and,
  col,
  eq,
  exists,
  field,
  fromSub,
  fromTable,
  gt,
  ident,
  inSubquery,
  param,
  render,
  scalar,
  select,
  verifyRoundTrip,
} from '../src/index.ts';

test('nested subqueries are numbered in deterministic traversal order', () => {
  const tenant = param('t1');
  const minAge = param(18);
  const lim = param(10);

  const inner = select({
    columns: [field('orders.user_id')],
    from: [ident('orders')],
    where: eq(field('orders.tenant'), tenant),
  });
  const q = select({
    columns: [field('u.id')],
    from: [fromTable(ident('users'), 'u')],
    where: and(inSubquery(field('u.id'), inner), gt(field('u.age'), minAge)),
    limit: lim,
  });

  const r = render(q);
  assert.equal(
    r.sql,
    'SELECT "u"."id" FROM "users" AS "u" ' +
      'WHERE (("u"."id" IN (SELECT "orders"."user_id" FROM "orders" WHERE ("orders"."tenant" = $1))) ' +
      'AND ("u"."age" > $2)) LIMIT $3',
  );
  assert.deepEqual(r.bindings, ['t1', 18, 10]);
  assert.deepEqual(r.emissionOrder, [1, 2, 3]);

  const v = verifyRoundTrip(q);
  assert.deepEqual(v.problems, []);
  assert.ok(v.ok);
});

test('a param shared across subquery boundaries keeps one placeholder', () => {
  const tenant = param('t1');
  const inner = select({
    columns: [field('orders.user_id')],
    from: [ident('orders')],
    where: eq(field('orders.tenant'), tenant),
  });
  const q = select({
    columns: [field('u.id')],
    from: [fromTable(ident('users'), 'u')],
    where: and(inSubquery(field('u.id'), inner), eq(field('u.tenant'), tenant)),
  });
  const r = render(q);
  assert.equal(
    r.sql,
    'SELECT "u"."id" FROM "users" AS "u" ' +
      'WHERE (("u"."id" IN (SELECT "orders"."user_id" FROM "orders" WHERE ("orders"."tenant" = $1))) ' +
      'AND ("u"."tenant" = $1))',
  );
  assert.deepEqual(r.bindings, ['t1']);
  assert.deepEqual(r.emissionOrder, [1, 1]);
  assert.ok(verifyRoundTrip(q).ok);
});

test('three levels of nesting: FROM subquery, scalar select, EXISTS', () => {
  const a = param('a');
  const b = param('b');
  const c = param('c');

  const level3 = select({
    columns: [field('z.id')],
    from: [ident('z')],
    where: eq(field('z.k'), a),
  });
  const level2 = select({
    columns: [col(scalar(select({
      columns: [field('y.v')],
      from: [ident('y')],
      where: and(exists(level3), eq(field('y.k'), b)),
    })), 's')],
    from: [ident('y')],
    where: eq(field('y.k'), b),
  });
  const q = select({
    columns: [field('w.id')],
    from: [fromSub(level2, 'w')],
    where: eq(field('w.k'), c),
  });

  const r = render(q);
  // Traversal: outer columns -> FROM subquery (level2 columns: scalar
  // subquery -> level3 inside EXISTS gets $1, then y.k = $2) -> level2
  // WHERE ($2 again) -> outer WHERE ($3).
  assert.equal(
    r.sql,
    'SELECT "w"."id" FROM (' +
      'SELECT (SELECT "y"."v" FROM "y" WHERE ((EXISTS (SELECT "z"."id" FROM "z" WHERE ("z"."k" = $1))) AND ("y"."k" = $2))) AS "s" ' +
      'FROM "y" WHERE ("y"."k" = $2)' +
      ') AS "w" WHERE ("w"."k" = $3)',
  );
  assert.deepEqual(r.bindings, ['a', 'b', 'c']);
  assert.deepEqual(r.emissionOrder, [1, 2, 2, 3]);

  const v = verifyRoundTrip(q);
  assert.deepEqual(v.problems, []);
  assert.ok(v.ok);
});
