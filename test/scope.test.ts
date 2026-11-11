import assert from 'node:assert/strict';
import test from 'node:test';
import {
  buildAndVerify,
  cmp,
  col,
  exists,
  inQuery,
  param,
  select,
  subquery,
  table,
} from '../index.ts';
import { whitelist } from './fixtures.ts';

test('allows a correlated EXISTS reference to an outer scope alias', () => {
  const correlated = select({
    selectList: [col('o', 'id')],
    from: table('orders', 'o'),
    where: cmp('=', col('o', 'user_id'), col('u', 'id')),
  });
  const q = select({
    selectList: [col('u', 'id')],
    from: table('users', 'u'),
    where: exists(correlated),
  });
  const report = buildAndVerify(q, whitelist);
  assert.match(report.sql, /EXISTS/);
  assert.equal(report.checks.subqueryScoping, 'passed');
});

test('rejects outer-scope references inside a non-correlated IN subquery', () => {
  const isolated = select({
    selectList: [col('user_id')],
    from: table('orders', 'o'),
    where: cmp('=', col('o', 'user_id'), col('u', 'id')),
  });
  const q = select({
    selectList: [col('id')],
    from: table('users', 'u'),
    where: inQuery(col('u', 'id'), isolated),
  });
  assert.throws(() => buildAndVerify(q, whitelist), /unknown column|unknown table qualifier/);
});

test('exposes derived-table output columns through their alias', () => {
  const derived = select({
    selectList: [
      col('user_id'),
      { expr: param('floor', 'number'), as: 'base' },
    ],
    from: table('orders'),
  });
  const q = select({
    selectList: [col('d', 'base')],
    from: subquery(derived, 'd'),
    where: cmp('>', col('d', 'user_id'), param('id', 'integer')),
  });
  const report = buildAndVerify(q, whitelist);
  assert.match(report.sql, /AS "d"/);
});

test('rejects references to columns not projected by a derived table', () => {
  const derived = select({
    selectList: [col('user_id')],
    from: table('orders'),
  });
  const q = select({
    selectList: [col('d', 'amount')],
    from: subquery(derived, 'd'),
  });
  assert.throws(() => buildAndVerify(q, whitelist), /identifier whitelist/);
});

test('rejects SELECT * inside a derived table', () => {
  const derived = select({
    selectList: [{ star: true }],
    from: table('orders'),
  });
  const q = select({
    selectList: [col('d', 'id')],
    from: subquery(derived, 'd'),
  });
  assert.throws(() => buildAndVerify(q, whitelist), /SELECT \*/);
});
