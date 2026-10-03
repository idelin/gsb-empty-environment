import { asc } from '../src/ordering.ts';
import { IndexScan, TableScan, MergeJoin, GroupBy } from '../src/operators.ts';
import { optimize } from '../src/optimizer.ts';

function show(title: string, sql: string, root: any, orderBy: any): void {
  const { plan, stats } = optimize(root, orderBy);
  console.log(`=== ${title} ===`);
  console.log(`SQL: ${sql}`);
  console.log('Physical plan:');
  console.log(plan.describe());
  console.log(
    `Stats: sortsInserted=${stats.sortsInserted}, sortsAvoided=${stats.sortsAvoided}\n`,
  );
}

// Acceptance scenario: ORDER BY u.id over IndexScan(u.id) -> Sort omitted.
show(
  'Acceptance: index already provides the required order',
  'SELECT * FROM users u ORDER BY u.id',
  new IndexScan('users', 'pk_users_id', [asc('u.id')]),
  [asc('u.id')],
);

// Same query without a usable index -> Sort enforcer inserted.
show(
  'Fallback: unordered table scan needs a Sort',
  'SELECT * FROM users u ORDER BY u.id',
  new TableScan('users'),
  [asc('u.id')],
);

// MergeJoin where both sides are covered by indexes -> no Sort at all.
show(
  'MergeJoin: both inputs already ordered by their indexes',
  'SELECT * FROM users u JOIN orders o ON u.id = o.user_id ORDER BY u.id',
  new MergeJoin(
    new IndexScan('users', 'pk_users_id', [asc('u.id')]),
    new IndexScan('orders', 'idx_orders_user_id', [asc('o.user_id')]),
    [asc('u.id')],
    [asc('o.user_id')],
  ),
  [asc('u.id')],
);

// GroupBy over an unordered scan -> Sort inserted below the aggregate.
show(
  'GroupBy: unordered input forces a Sort enforcer',
  'SELECT u.country, COUNT(*) FROM users u GROUP BY u.country',
  new GroupBy(new TableScan('users'), [asc('u.country')]),
  null,
);
