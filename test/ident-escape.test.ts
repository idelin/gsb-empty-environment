import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  col,
  eq,
  field,
  fromTable,
  ident,
  param,
  quoteIdent,
  render,
  select,
  verifyRoundTrip,
} from '../src/index.ts';

test('identifiers are double-quoted with embedded quotes doubled', () => {
  assert.equal(quoteIdent('plain'), '"plain"');
  assert.equal(quoteIdent('we"ird'), '"we""ird"');
  assert.equal(quoteIdent('a"b"c'), '"a""b""c"');
  assert.equal(quoteIdent('has space'), '"has space"');
  assert.equal(quoteIdent('select'), '"select"');
});

test('qualified and special field names render escaped', () => {
  const q = select({
    columns: [
      ident('we"ird', 'na me', 'select'),
      col(field('t.id'), 'cnt"1'),
    ],
    from: [fromTable(ident('my table'), 'a b')],
    where: eq(ident('t', 'quo"te'), param(1)),
  });
  const r = render(q);
  assert.equal(
    r.sql,
    'SELECT "we""ird"."na me"."select", "t"."id" AS "cnt""1" ' +
      'FROM "my table" AS "a b" WHERE ("t"."quo""te" = $1)',
  );
});

test('escaped identifiers round-trip through reparse unchanged', () => {
  const q = select({
    columns: [
      ident('we"ird', 'na me', 'select'),
      col(field('t.id'), 'cnt"1'),
    ],
    from: [fromTable(ident('my table'), 'a b')],
    where: eq(ident('t', 'quo"te'), param(1)),
  });
  const v = verifyRoundTrip(q);
  assert.deepEqual(v.problems, []);
  assert.ok(v.ok);
});

test('keyword-like and dotted names stay a single identifier part', () => {
  const q = select({
    columns: [ident('order'), ident('a.b')],
    from: [ident('from')],
  });
  const r = render(q);
  // ident('a.b') is one part containing a dot; field('a.b') would be two.
  assert.equal(r.sql, 'SELECT "order", "a.b" FROM "from"');
  const v = verifyRoundTrip(q);
  assert.deepEqual(v.problems, []);
  assert.ok(v.ok);
});
