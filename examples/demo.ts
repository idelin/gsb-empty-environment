import { demoCatalog } from '../src/catalog.ts';
import { formatPlan } from '../src/format.ts';
import { optimize } from '../src/optimizer.ts';
import { parse } from '../src/parser.ts';
import { countNodes } from '../src/plan.ts';

const catalog = demoCatalog();

const queries = [
  // 验收边界: ORDER BY u.id + 底层 IndexScan(u.id) → 省去 Sort
  'SELECT u.id, u.name FROM users u ORDER BY u.id',
  // 无索引字段排序 → 插入 Sort enforcer
  'SELECT u.id FROM users u ORDER BY u.name',
  // GroupBy 要求有序，索引直接满足
  'SELECT u.id, count(*) FROM users u GROUP BY u.id',
  // MergeJoin: users 侧走索引，orders 侧补一个 Sort
  'SELECT u.id, o.id FROM users u JOIN orders o ON u.id = o.user_id ORDER BY u.id',
];

for (const sql of queries) {
  const physical = optimize(parse(sql), catalog);
  const sorts = countNodes(physical, 'Sort');
  console.log(`SQL: ${sql}`);
  console.log(formatPlan(physical));
  console.log(`--> physical Sort operators: ${sorts}${sorts === 0 ? '  (eliminated!)' : ''}`);
  console.log('');
}
