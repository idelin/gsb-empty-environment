/**
 * 演示验收边界场景：
 *   事务 A 持有表 T 中行 R1 的 X 锁（表 T 自动带 IX）；
 *   事务 B 请求表 T 的整表 X 锁；
 *   系统由 IX 与 X 互斥直接快速阻塞 B，不遍历表内任何行。
 */
import { LockManager } from '../src/mgl.ts';

const lm = new LockManager();
const DB = 'shop';
const TBL = 'orders';

function show(title: string): void {
  console.log(`\n=== ${title} ===`);
  const snap = lm.snapshot();
  for (const [key, holders] of Object.entries(snap)) {
    console.log(`  ${key.padEnd(34)} ${JSON.stringify(holders)}`);
  }
}

// 1) 事务 A 锁定行 R1（X），意向锁自动沿层次传播
await lm.lockRow('A', DB, TBL, 'R1', 'X');
show('事务 A 持有 R1 的 X 锁（表/库自动带 IX）');

// 2) 事务 B 请求整表 X —— 被表节点上的 IX 直接阻塞
let bGranted = false;
const bRequest = lm.lockTable('B', DB, TBL, 'X').then(() => {
  bGranted = true;
  console.log('  >> 事务 B 获得表 orders 的 X 锁');
});
await new Promise((r) => setTimeout(r, 20));
console.log(`\n事务 B 请求整表 X：${bGranted ? '已授予' : '被阻塞（IX 与 X 互斥）'}`);
console.log(`  表节点等待队列长度: ${lm.queueLength(LockManager.tableKey(DB, TBL))}`);
console.log(`  冲突判定中访问的行节点数: ${lm.stats.rowNodesExamined}（无需遍历全表）`);
console.log(`  冲突判定次数: ${lm.stats.conflictChecks}，检查聚合条目: ${lm.stats.aggregateEntriesExamined}（每次 <= 5，O(1)）`);

// 3) 事务 A 提交，释放全部锁，B 被唤醒
console.log('\n事务 A 提交，释放全部锁 ...');
lm.releaseAll('A');
await bRequest;
show('事务 B 获得整表 X 锁后的锁状态');
