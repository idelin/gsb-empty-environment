import test from 'node:test';
import assert from 'node:assert/strict';
import {
  and,
  eq,
  field,
  inList,
  inSubquery,
  notInList,
  or,
  param,
  parse,
  raw,
  render,
  select,
  verifyRoundTrip,
} from '../src/index.ts';

test('render-reparse verifies binding correspondence on a complex query', () => {
  const shared = param(42);
  const inner = select(field('uid'))
    .from('orders')
    .where(and(eq(field('total'), param(9.5)), eq(field('flag'), shared)));
  const q = select(field('id'), raw('COUNT(*)'))
    .from('users')
    .where(
      or(
        and(eq(field('status'), param('active')), inList(field('age'), [param(1), param(2)])),
        inSubquery(field('id'), inner),
        eq(field('score'), shared),
      ),
    )
    .orderBy(field('id'), 'DESC')
    .limit(param(10));
  const report = verifyRoundTrip(q);
  assert.deepEqual(report.errors, []);
  assert.equal(report.ok, true);
  // shared node appears twice in traversal but binds a single value slot
  assert.deepEqual(report.params, ['active', 1, 2, 9.5, 42, 10]);
  const sharedRecords = report.bindings.filter((b) => b.value === 42);
  assert.equal(sharedRecords.length, 2);
  assert.equal(sharedRecords[0].placeholder, sharedRecords[1].placeholder);
});

test('empty IN list renders as constant false and binds nothing', () => {
  const q = select(field('id')).from('t').where(inList(field('id'), []));
  const { sql, params } = render(q);
  assert.equal(sql, 'SELECT "id" FROM "t" WHERE (1 = 0)');
  assert.deepEqual(params, []);
  const report = verifyRoundTrip(q);
  assert.equal(report.ok, true);
  const parsed = parse(sql);
  assert.deepEqual(parsed.where, { kind: 'const', value: false });
});

test('empty NOT IN list renders as constant true', () => {
  const q = select(field('id')).from('t').where(notInList(field('id'), []));
  const { sql, params } = render(q);
  assert.equal(sql, 'SELECT "id" FROM "t" WHERE (1 = 1)');
  assert.deepEqual(params, []);
  const parsed = parse(sql);
  assert.deepEqual(parsed.where, { kind: 'const', value: true });
});

test('empty junctions render as constants and survive reparse', () => {
  const qAnd = select(field('id')).from('t').where(and());
  const qOr = select(field('id')).from('t').where(or());
  assert.equal(render(qAnd).sql, 'SELECT "id" FROM "t" WHERE (1 = 1)');
  assert.equal(render(qOr).sql, 'SELECT "id" FROM "t" WHERE (1 = 0)');
  assert.equal(verifyRoundTrip(qAnd).ok, true);
  assert.equal(verifyRoundTrip(qOr).ok, true);
});

test('field names are escaped and recovered by reparse', () => {
  const q = select(field('we"ird'), field('u.name'))
    .from('my"table')
    .where(eq(field('u.name'), param('x')));
  const { sql } = render(q);
  assert.equal(
    sql,
    'SELECT "we""ird", "u"."name" FROM "my""table" WHERE "u"."name" = $1',
  );
  const parsed = parse(sql);
  assert.deepEqual(parsed.select, [
    { kind: 'field', path: ['we"ird'] },
    { kind: 'field', path: ['u', 'name'] },
  ]);
  assert.deepEqual(parsed.from, { kind: 'table', path: ['my"table'] });
  const report = verifyRoundTrip(q);
  assert.equal(report.ok, true);
});

test('reparse recovers raw select items and placeholder order', () => {
  const q = select(raw('COUNT(*)'), field('id'))
    .from('t')
    .where(and(eq(field('a'), param('p1')), eq(field('b'), param('p2'))));
  const parsed = parse(render(q).sql);
  assert.deepEqual(parsed.select[0], { kind: 'raw', text: 'COUNT(*)' });
  const report = verifyRoundTrip(q);
  assert.deepEqual(
    report.bindings.map((b) => [b.placeholder, b.bound]),
    [
      [1, 'p1'],
      [2, 'p2'],
    ],
  );
});
