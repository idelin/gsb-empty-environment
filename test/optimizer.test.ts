import { test } from 'node:test';
import assert from 'node:assert/strict';
import { demoCatalog } from '../src/catalog.ts';
import { optimize } from '../src/optimizer.ts';
import { parse } from '../src/parser.ts';
import { countNodes, walk, type PhysicalNode } from '../src/plan.ts';
import { satisfies, toOrdering } from '../src/properties.ts';

const catalog = demoCatalog();
const plan = (sql: string) => optimize(parse(sql), catalog);

function find<T extends PhysicalNode['kind']>(
  node: PhysicalNode,
  kind: T,
): Extract<PhysicalNode, { kind: T }> | undefined {
  for (const n of walk(node)) if (n.kind === kind) return n as never;
  return undefined;
}

test('satisfies: prefix rule', () => {
  const delivered = toOrdering(['u.id', 'u.name']);
  assert.ok(satisfies(delivered, toOrdering(['u.id'])));
  assert.ok(satisfies(delivered, toOrdering(['u.id', 'u.name'])));
  assert.ok(!satisfies(delivered, toOrdering(['u.name'])));
  assert.ok(!satisfies(toOrdering(['u.id']), toOrdering(['u.id', 'u.name'])));
  assert.ok(satisfies(delivered, []));
  assert.ok(!satisfies([], toOrdering(['u.id'])));
});

test('ACCEPTANCE: ORDER BY u.id over IndexScan(u.id) eliminates the Sort', () => {
  const physical = plan('SELECT u.id, u.name FROM users u ORDER BY u.id');
  assert.equal(countNodes(physical, 'Sort'), 0, 'no physical Sort may remain');
  const scan = find(physical, 'IndexScan');
  assert.ok(scan, 'must use IndexScan on users.id');
  assert.equal(scan.index, 'users_pkey');
  assert.equal(countNodes(physical, 'SeqScan'), 0);
});

test('ORDER BY u.id, u.name uses composite index (id, name), no Sort', () => {
  const physical = plan('SELECT u.id FROM users u ORDER BY u.id, u.name');
  assert.equal(countNodes(physical, 'Sort'), 0);
  assert.equal(find(physical, 'IndexScan')!.index, 'users_id_name_idx');
});

test('ORDER BY u.name without matching index inserts a Sort enforcer', () => {
  const physical = plan('SELECT u.id FROM users u ORDER BY u.name');
  assert.equal(countNodes(physical, 'Sort'), 1);
  const sort = find(physical, 'Sort');
  assert.deepEqual(sort!.keys, toOrdering(['u.name']));
  assert.ok(find(physical, 'SeqScan'), 'falls back to SeqScan + Sort');
});

test('ORDER BY u.id DESC: ascending index does not satisfy, Sort inserted', () => {
  const physical = plan('SELECT u.id FROM users u ORDER BY u.id DESC');
  assert.equal(countNodes(physical, 'Sort'), 1);
});

test('GROUP BY u.id with index: StreamAggregate without Sort', () => {
  const physical = plan('SELECT u.id, count(*) FROM users u GROUP BY u.id');
  assert.ok(find(physical, 'StreamAggregate'));
  assert.equal(countNodes(physical, 'Sort'), 0);
  assert.ok(find(physical, 'IndexScan'));
});

test('GROUP BY o.user_id without index: Sort inserted below StreamAggregate', () => {
  const physical = plan('SELECT o.user_id, count(*) FROM orders o GROUP BY o.user_id');
  assert.equal(countNodes(physical, 'Sort'), 1);
  const agg = find(physical, 'StreamAggregate')!;
  assert.equal(agg.child.kind, 'Sort', 'Sort must sit directly below the aggregate');
});

test('MergeJoin: indexed side reused, other side gets exactly one Sort', () => {
  const physical = plan(
    'SELECT u.id, o.id FROM users u JOIN orders o ON u.id = o.user_id',
  );
  const join = find(physical, 'MergeJoin');
  assert.ok(join, 'one natively-ordered side is enough to pick MergeJoin');
  assert.equal(join.left.kind, 'IndexScan', 'users side comes straight from the index');
  assert.equal(join.right.kind, 'Sort', 'orders side needs a Sort enforcer');
  assert.equal(countNodes(physical, 'Sort'), 1);
});

test('MergeJoin output order satisfies a downstream ORDER BY on the join key', () => {
  const physical = plan(
    'SELECT u.id, o.id FROM users u JOIN orders o ON u.id = o.user_id ORDER BY u.id',
  );
  assert.ok(find(physical, 'MergeJoin'));
  assert.equal(countNodes(physical, 'Sort'), 1, 'only the orders-side Sort remains');
});

test('HashJoin when neither side is ordered; ORDER BY then needs one Sort', () => {
  const physical = plan(
    'SELECT o.id, o2.id FROM orders o JOIN orders o2 ON o.user_id = o2.user_id ORDER BY o.user_id',
  );
  assert.ok(find(physical, 'HashJoin'));
  assert.equal(countNodes(physical, 'Sort'), 1);
});

test('Filter and Project are order-preserving (no spurious Sort)', () => {
  const physical = plan(
    'SELECT u.id FROM users u WHERE u.age > 18 ORDER BY u.id',
  );
  assert.equal(countNodes(physical, 'Sort'), 0);
  assert.ok(find(physical, 'IndexScan'));
  assert.ok(find(physical, 'Filter'));
});
