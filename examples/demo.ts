/**
 * 运行样例：验收场景复现。
 * 事务 A 持有表 orders 中行 R1 的 X 锁（表/库自动带 IX），
 * 事务 B 请求整表 X 锁 —— 系统由 IX 与 X 互斥直接快速阻塞 B，不遍历行。
 */
import { LockManager, Transaction, table } from '../src/lock-manager.ts';

const lm = new LockManager();
const a = new Transaction(lm);
const b = new Transaction(lm);

console.log('=== 步骤 1：事务 A 对 orders.R1 加行级 X 锁 ===');
await a.lockRow('shop', 'orders', 'R1', 'X');
console.log('锁表快照:', JSON.stringify(lm.snapshot(), null, 2));

console.log('\n=== 步骤 2：事务 B 请求 orders 整表 X 锁 ===');
const rowChecksBefore = lm.stats.conflictChecks.row;
let bGranted = false;
const bRequest = b.lockTable('shop', 'orders', 'X').then(() => {
  bGranted = true;
});
await new Promise((r) => setTimeout(r, 50));

console.log(`B 是否被授予: ${bGranted}（预期 false，被阻塞排队）`);
console.log(`B 是否在等待队列: ${lm.isWaiting(b.id)}`);
console.log(`判定原因: 表 orders 上 A 持有的 IX 与 B 请求的 X 互斥`);
console.log(`行级条目被检查次数: ${lm.stats.conflictChecks.row - rowChecksBefore}（预期 0，未遍历行）`);
console.log('锁表快照:', JSON.stringify(lm.snapshot(), null, 2));

console.log('\n=== 步骤 3：事务 A 提交，调度器唤醒 B ===');
a.commit();
await bRequest;
console.log(`B 是否被授予: ${bGranted}（预期 true）`);
console.log(`B 在表 orders 上的锁模式: ${lm.heldMode(b.id, table('shop', 'orders'))}`);
b.commit();
console.log('全部锁已释放，锁表快照:', JSON.stringify(lm.snapshot()));
