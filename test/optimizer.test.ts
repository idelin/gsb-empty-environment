import { test } from 'node:test';
import assert from 'node:assert/strict';

import { asc, desc, satisfies } from '../src/ordering.ts';
import {
  GroupBy,
  IndexScan,
  MergeJoin,
  PhysicalNode,
  Sort,
  TableScan,
} from '../src/operators.ts';
import { optimize } from '../src/optimizer.ts';

function countSorts(node: PhysicalNode): number {
  return (
    (node instanceof Sort ? 1 : 0) +
    node.children.reduce((sum, child) => sum + countSorts(child), 0)
  );
}

test('ordering: prefix satisfaction rules', () => {
  assert.equal(satisfies([asc('u.id')], [asc('u.id')]), true);
  assert.equal(satisfies([asc('u.id'), asc('u.name')], [asc('u.id')]), true);
  assert.equal(satisfies([asc('u.id')], [asc('u.id'), asc('u.name')]), false);
  assert.equal(satisfies([asc('u.id')], [desc('u.id')]), false);
  assert.equal(satisfies([], [asc('u.id')]), false);
});

test('acceptance: ORDER BY u.id over IndexScan(u.id) omits the Sort operator', () => {
  const scan = new IndexScan('users', 'pk_users_id', [asc('u.id')]);
  const { plan, stats } = optimize(scan, [asc('u.id')]);

  assert.equal(countSorts(plan), 0);
  assert.equal(stats.sortsInserted, 0);
  assert.equal(stats.sortsAvoided, 1);
  assert.equal(plan, scan, 'root stays the IndexScan, no Sort wrapper');
});

test('ORDER BY u.id over unordered TableScan inserts a Sort enforcer', () => {
  const { plan, stats } = optimize(new TableScan('users'), [asc('u.id')]);

  assert.equal(countSorts(plan), 1);
  assert.equal(stats.sortsInserted, 1);
  assert.ok(plan instanceof Sort);
  assert.ok(plan.children[0] instanceof TableScan);
});

test('direction mismatch (index ASC vs ORDER BY DESC) still requires a Sort', () => {
  const scan = new IndexScan('users', 'pk_users_id', [asc('u.id')]);
  const { plan, stats } = optimize(scan, [desc('u.id')]);

  assert.equal(countSorts(plan), 1);
  assert.equal(stats.sortsInserted, 1);
});

test('composite index satisfies a prefix ORDER BY without a Sort', () => {
  const scan = new IndexScan('users', 'idx_id_name', [asc('u.id'), asc('u.name')]);
  const { stats } = optimize(scan, [asc('u.id')]);

  assert.equal(stats.sortsInserted, 0);
});

test('MergeJoin with both sides index-ordered inserts no Sorts', () => {
  const join = new MergeJoin(
    new IndexScan('users', 'pk_users_id', [asc('u.id')]),
    new IndexScan('orders', 'idx_orders_user_id', [asc('o.user_id')]),
    [asc('u.id')],
    [asc('o.user_id')],
  );
  const { plan, stats } = optimize(join, [asc('u.id')]);

  assert.equal(countSorts(plan), 0);
  assert.equal(stats.sortsInserted, 0);
  assert.equal(stats.sortsAvoided, 3, 'left keys, right keys, top ORDER BY');
});

test('MergeJoin over unordered scans inserts one Sort per input', () => {
  const join = new MergeJoin(
    new TableScan('users'),
    new TableScan('orders'),
    [asc('u.id')],
    [asc('o.user_id')],
  );
  const { plan, stats } = optimize(join, [asc('u.id')]);

  assert.equal(stats.sortsInserted, 2);
  assert.equal(countSorts(plan), 2);
  const [left, right] = plan.children;
  assert.ok(left instanceof Sort);
  assert.ok(right instanceof Sort);
});

test('GroupBy over unordered scan gets a Sort below the aggregate', () => {
  const groupBy = new GroupBy(new TableScan('users'), [asc('u.country')]);
  const { plan, stats } = optimize(groupBy, null);

  assert.equal(stats.sortsInserted, 1);
  assert.ok(plan instanceof GroupBy);
  assert.ok(plan.children[0] instanceof Sort);
});

test('GroupBy over a matching index reuses the index order', () => {
  const groupBy = new GroupBy(
    new IndexScan('users', 'idx_users_country', [asc('u.country')]),
    [asc('u.country')],
  );
  const { plan, stats } = optimize(groupBy, null);

  assert.equal(stats.sortsInserted, 0);
  assert.equal(countSorts(plan), 0);
});

test('GroupBy output order also satisfies a downstream ORDER BY on the keys', () => {
  const groupBy = new GroupBy(
    new IndexScan('users', 'idx_users_country', [asc('u.country')]),
    [asc('u.country')],
  );
  const { stats } = optimize(groupBy, [asc('u.country')]);

  assert.equal(stats.sortsInserted, 0);
});
