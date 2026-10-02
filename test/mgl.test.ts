import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  LockManager,
  LockTimeoutError,
  compatible,
  combineModes,
  type LockMode,
} from '../src/mgl.ts';

const DB = 'shop';
const TBL = 'orders';
const tblKey = LockManager.tableKey(DB, TBL);
const dbKey = LockManager.dbKey(DB);

/** 让微任务队列空转几轮，确保已解决的 Promise 回调得到执行。 */
async function flush(): Promise<void> {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

test('兼容矩阵符合 MGL 规范', () => {
  const all: LockMode[] = ['IS', 'IX', 'S', 'SIX', 'X'];
  // 对称性
  for (const a of all) for (const b of all) {
    assert.equal(compatible(a, b), compatible(b, a), `${a} vs ${b}`);
  }
  // 关键互斥对
  assert.equal(compatible('IX', 'X'), false, 'IX 与 X 互斥（验收边界的判定依据）');
  assert.equal(compatible('IX', 'S'), false);
  assert.equal(compatible('S', 'X'), false);
  assert.equal(compatible('X', 'X'), false);
  // 关键兼容对
  assert.equal(compatible('IX', 'IX'), true, '多事务可同时对不同行加 X');
  assert.equal(compatible('IS', 'S'), true);
  assert.equal(compatible('S', 'S'), true);
  assert.equal(compatible('IS', 'IX'), true);
});

test('行级 X 锁自动在表和库上传播 IX；行级 S 锁传播 IS', async () => {
  const lm = new LockManager();
  await lm.lockRow('A', DB, TBL, 'R1', 'X');
  assert.equal(lm.holdersOf(dbKey).get('A'), 'IX');
  assert.equal(lm.holdersOf(tblKey).get('A'), 'IX');
  assert.equal(lm.holdersOf(LockManager.rowKey(DB, TBL, 'R1')).get('A'), 'X');

  await lm.lockRow('B', DB, TBL, 'R2', 'S');
  assert.equal(lm.holdersOf(dbKey).get('B'), 'IS');
  assert.equal(lm.holdersOf(tblKey).get('B'), 'IS');
  // 表节点聚合计数：IX=1, IS=1 —— 后代行锁的摘要
  const counts = lm.aggregateCounts(tblKey);
  assert.equal(counts.IX, 1);
  assert.equal(counts.IS, 1);
});

test('验收边界：表级 X 请求被行级 X 的 IX 快速阻塞，零行遍历', async () => {
  const lm = new LockManager();

  // 事务 A：锁定表 T 中的行 R1（X），表 T 自动带上 IX
  await lm.lockRow('A', DB, TBL, 'R1', 'X');
  assert.equal(lm.aggregateCounts(tblKey).IX, 1, '表 T 应带 IX');

  // 事务 B：请求表 T 的整表 X 锁
  let bGranted = false;
  const bRequest = lm.lockTable('B', DB, TBL, 'X').then(() => { bGranted = true; });
  await flush();

  // 1) B 被阻塞：IX 与 X 互斥，直接判定
  assert.equal(bGranted, false, 'B 的表级 X 必须被阻塞');
  assert.equal(lm.queueLength(tblKey), 1, 'B 应在表节点的等待队列中');

  // 2) 快速判定：冲突检查只读表节点聚合计数，行节点访问数恒为 0
  assert.equal(lm.stats.rowNodesExamined, 0, '不得遍历任何行节点');
  assert.ok(lm.stats.conflictChecks > 0);
  assert.ok(
    lm.stats.aggregateEntriesExamined <= lm.stats.conflictChecks * 5,
    '每次冲突判定最多检查 5 个聚合条目（O(1)）',
  );

  // 3) 即使表内有大量其它行节点存在，判定同样不触碰它们
  for (let i = 0; i < 1000; i++) {
    lm.holdersOf(LockManager.rowKey(DB, TBL, `R${i + 2}`)); // 仅创建节点，模拟大行数
  }
  assert.equal(lm.stats.rowNodesExamined, 0, '行数多少与判定成本无关');

  // 4) A 提交释放后，B 被唤醒并获得整表 X
  lm.releaseAll('A');
  await bRequest;
  assert.equal(bGranted, true, 'A 释放后 B 应获得表级 X');
  assert.equal(lm.holdersOf(tblKey).get('B'), 'X');
});

test('行级 S 锁可共享；表级 S 与行级 S 共存', async () => {
  const lm = new LockManager();
  await lm.lockRow('A', DB, TBL, 'R1', 'S');
  await lm.lockRow('B', DB, TBL, 'R1', 'S'); // 同行共享
  await lm.lockTable('C', DB, TBL, 'S');      // 表 S 与行 S 的 IS 兼容
  assert.equal(lm.holdersOf(LockManager.rowKey(DB, TBL, 'R1')).size, 2);
  assert.equal(lm.holdersOf(tblKey).get('C'), 'S');
});

test('表级 S 阻塞其它事务的行级 X（IX 与 S 互斥）', async () => {
  const lm = new LockManager();
  await lm.lockTable('A', DB, TBL, 'S');

  let bGranted = false;
  const p = lm.lockRow('B', DB, TBL, 'R1', 'X').then(() => { bGranted = true; });
  await flush();
  assert.equal(bGranted, false, 'B 需要表级 IX，与 A 的表级 S 冲突');

  lm.releaseAll('A');
  await p;
  assert.equal(bGranted, true);
});

test('不同行的行级 X 锁互不阻塞（IX 与 IX 兼容）', async () => {
  const lm = new LockManager();
  await lm.lockRow('A', DB, TBL, 'R1', 'X');
  await lm.lockRow('B', DB, TBL, 'R2', 'X'); // 不应阻塞
  assert.equal(lm.holdersOf(LockManager.rowKey(DB, TBL, 'R2')).get('B'), 'X');
  assert.equal(lm.aggregateCounts(tblKey).IX, 2);
});

test('同一事务锁升级：表级 S 升级为 X（经 SIX 合并语义）', async () => {
  const lm = new LockManager();
  await lm.lockTable('A', DB, TBL, 'S');
  await lm.lockTable('A', DB, TBL, 'X'); // 自己持有的 S 不阻塞自己的 X
  assert.equal(lm.holdersOf(tblKey).get('A'), 'X');
  assert.equal(combineModes('S', 'IX'), 'SIX');
  assert.equal(combineModes('S', 'X'), 'X');
  assert.equal(combineModes('IS', 'X'), 'X');
});

test('FIFO 公平：后来的兼容请求不得插队越过等待中的 X', async () => {
  const lm = new LockManager();
  await lm.lockRow('A', DB, TBL, 'R1', 'S');

  const order: string[] = [];
  const bx = lm.lockRow('B', DB, TBL, 'R1', 'X').then(() => { order.push('B'); });
  const cs = lm.lockRow('C', DB, TBL, 'R1', 'S').then(() => { order.push('C'); });
  await flush();
  assert.deepEqual(order, [], 'B、C 均在等待');

  lm.releaseAll('A');
  await bx;
  await flush();
  assert.deepEqual(order, ['B'], 'C 不得越过先排队的 B');
  lm.releaseAll('B');
  await cs;
  assert.deepEqual(order, ['B', 'C']);
});

test('等待超时：请求被移除并抛出 LockTimeoutError', async () => {
  const lm = new LockManager();
  await lm.lockRow('A', DB, TBL, 'R1', 'X');
  await assert.rejects(
    lm.lockTable('B', DB, TBL, 'X', { timeoutMs: 30 }),
    LockTimeoutError,
  );
  assert.equal(lm.queueLength(tblKey), 0, '超时后请求应出队');
  // 超时请求不再占用队列，A 释放后第三方可立即获锁
  lm.releaseAll('A');
  await lm.lockTable('C', DB, TBL, 'X');
  assert.equal(lm.holdersOf(tblKey).get('C'), 'X');
});

test('releaseAll 释放事务在全部层级上的锁', async () => {
  const lm = new LockManager();
  await lm.lockRow('A', DB, TBL, 'R1', 'X');
  lm.releaseAll('A');
  assert.equal(lm.holdersOf(dbKey).size, 0);
  assert.equal(lm.holdersOf(tblKey).size, 0);
  assert.equal(lm.holdersOf(LockManager.rowKey(DB, TBL, 'R1')).size, 0);
});
