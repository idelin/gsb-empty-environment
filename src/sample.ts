/**
 * 运行样例（对应验收边界）：
 *   - users 表有 20 个字段
 *   - 顶层 Project 只消费 id 和 name
 *   - 中间 Join 只需要 id 作为连接键
 *   - 修剪后底层 Scan 的输出模式被压缩为仅 [id, name]
 */
import { FieldRef } from './expression.ts';
import { Join, Project, Scan } from './operators.ts';
import { ProjectionPruner } from './pruner.ts';

// 20 个字段的全表
const ALL_COLUMNS = Array.from({ length: 20 }, (_, i) => `col${String(i + 1).padStart(2, '0')}`);
ALL_COLUMNS[0] = 'id';
ALL_COLUMNS[1] = 'name';

const leftScan = new Scan('users', ALL_COLUMNS);
const rightScan = new Scan('orders', ['id', 'user_id', 'amount', 'status', 'created_at']);

// 顶层只需 id、name；Join 只需 id 做连接键
const plan = new Project(
  [
    { name: 'id', expr: new FieldRef('id') },
    { name: 'name', expr: new FieldRef('name') },
  ],
  new Join(leftScan, rightScan, ['id'], ['user_id']),
);

console.log('=== 修剪前 ===');
console.log(plan.describe());
console.log(`users Scan 输出列数: ${leftScan.outputSchema().length}`);

ProjectionPruner.prune(plan);

console.log('\n=== 修剪后 ===');
console.log(plan.describe());
console.log(`users Scan 输出列数: ${leftScan.outputSchema().length}`);
console.log(`users Scan 输出模式: [${leftScan.outputSchema().join(', ')}]`);
