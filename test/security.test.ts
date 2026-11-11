import assert from 'node:assert/strict';
import test from 'node:test';
import {
  buildAndVerify,
  MAX_LIMIT,
  MAX_ORDER_ITEMS,
  QueryStructureError,
  asc,
  cmp,
  col,
  desc,
  fn,
  inList,
  param,
  select,
  table,
} from '../index.ts';
import { SqlParser } from '../src/parser/sql-parser.ts';
import { whitelist } from './fixtures.ts';

function build(q: Parameters<typeof buildAndVerify>[0]) {
  return buildAndVerify(q, whitelist);
}

test('rejects a table that is not in the identifier whitelist', () => {
  const q = select({
    selectList: [col('id')],
    from: table('secrets'),
  });
  assert.throws(() => build(q), /identifier whitelist/);
});

test('rejects a column that is not covered by the whitelist', () => {
  const q = select({
    selectList: [col('password_hash')],
    from: table('users'),
  });
  assert.throws(() => build(q), /unknown column/);
});

test('rejects ambiguous unqualified columns when joins overlap', () => {
  const q = select({
    selectList: [col('id')],
    from: table('users', 'u'),
    joins: [
      {
        type: 'inner',
        relation: table('orders', 'o'),
        on: cmp('=', col('o', 'user_id'), col('u', 'id')),
      },
    ],
  });
  assert.throws(() => build(q), /ambiguous/);
});

test('rejects unknown table qualifiers', () => {
  const q = select({
    selectList: [col('evil', 'id')],
    from: table('users', 'u'),
  });
  assert.throws(() => build(q), /unknown table qualifier/);
});

test('rejects functions outside the allowed set', () => {
  assert.throws(() => fn('LOAD_FILE', [param('p', 'string')]), /function not allowed/);
});

test('rejects non-parameter values inside an IN list', () => {
  assert.throws(
    () => inList(col('id'), [param('a', 'integer'), 1 as never]),
    /typed param\(\) nodes/,
  );
});

test('enforces the bounded ORDER BY item limit', () => {
  const orderBy = Array.from({ length: MAX_ORDER_ITEMS + 1 }, (_, index) =>
    index % 2 === 0 ? asc(col('id')) : desc(col('name')),
  );
  const q = select({
    selectList: [col('id'), col('name')],
    from: table('users'),
    orderBy,
  });
  assert.throws(() => build(q), new RegExp(`bounded to ${MAX_ORDER_ITEMS}`));
});

test('rejects limits outside the safe range', () => {
  assert.throws(
    () =>
      select({
        selectList: [col('id')],
        from: table('users'),
        limit: MAX_LIMIT + 1,
      }),
    /limit must be an integer/,
  );
});

test('rejects repeated parameters with conflicting types', () => {
  const q = select({
    selectList: [col('id')],
    from: table('users'),
    where: cmp(
      '=',
      fn('COALESCE', [param('shared', 'string'), param('shared', 'integer')]),
      param('other', 'string'),
    ),
  });
  assert.throws(() => build(q), /conflicting types/);
});

test('independent parser refuses inline string literals', () => {
  assert.throws(
    () => new SqlParser(`SELECT "id" FROM "users" WHERE "name" = 'bob'`).parseSelectStatement(),
    /inline string literals are forbidden/,
  );
});

test('independent parser refuses unquoted identifiers', () => {
  assert.throws(
    () => new SqlParser('SELECT id FROM users').parseSelectStatement(),
    /identifiers must be double quoted/,
  );
});

test('independent parser rejects unterminated quoted identifiers', () => {
  assert.throws(
    () => new SqlParser('SELECT "id FROM "users"').parseSelectStatement(),
    /unterminated quoted identifier/,
  );
});

test('rendered SQL never exposes a raw-text concatenation surface', async () => {
  const source = await Promise.all([
    import('../src/render.ts'),
    import('../src/ast.ts'),
  ]);
  const rendererExports = Object.keys(source[0]);
  assert.ok(!rendererExports.some((name) => /raw|text|concat|literal/i.test(name)));
  assert.ok(QueryStructureError !== undefined);
});
