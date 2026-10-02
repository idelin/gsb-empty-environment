import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  LockManager,
  Transaction,
  DeadlockError,
  compatible,
  combine,
  database,
  table,
  row,
} from '../src/lock-manager.ts';

/** 在 ms 内未兑现则判定为“被阻塞”。 */
async function isBlocked(p: Promise<void>, ms = 50): Promise<boolean> {
  const timeout = new Promise<'timeout'>((r) => setTimeout(() => r('timeout'), ms));
  const result = await Promise.race([p.then(() => 'granted' as const), timeout]);
  return result === 'timeout';
}

test('相容矩阵：IX 与 X 互斥，IX 与 IX 相容', () => {
  assert.equal(compatible('IX', 'X'), false);
  assert.equal(compatible('IX', 'IX'), true);
  assert.equal(compatible('IS', 'S'), true);
  assert.equal(compatible('S', 'X'), false);
  assert.equal(compatible('SIX', 'IS'), true);
  assert.equal(compatible('X', 'IS'), false);
});

test('锁模式叠加：S + IX = SIX', () => {
  assert.equal(combine('S', 'IX'), 'SIX');
  assert.equal(combine('IS', 'X'), 'X');
  assert.equal(combine('S', 'S'), 'S');
});

test('行级 X 锁自动在表与库上加 IX 意向锁', async () => {
  const lm = new LockManager();
  const a = new Transaction(lm);
  await a.lockRow('shop', 'orders', 'R1', 'X');
  assert.equal(lm.heldMode(a.id, database('shop')), 'IX');
  assert.equal(lm.heldMode(a.id, table('shop', 'orders')), 'IX');
  assert.equal(lm.heldMode(a.id, row('shop', 'orders', 'R1')), 'X');
});

test('行级 S 锁自动在表与库上加 IS 意向锁', async () => {
  const lm = new LockManager();
  const a = new Transaction(lm);
  await a.lockRow('shop', 'orders', 'R1', 'S');
  assert.equal(lm.heldMode(a.id, database('shop')), 'IS');
  assert.equal(lm.heldMode(a.id, table('shop', 'orders')), 'IS');
  assert.equal(lm.heldMode(a.id, row('shop', 'orders', 'R1')), 'S');
});

test('验收场景：A 持行 R1 的 X 锁（表带 IX），B 的整表 X 请求被快速阻塞且不遍历行', async () => {
  const lm = new LockManager();
  const a = new Transaction(lm);
  const b = new Transaction(lm);

  // A 锁定大量行，制造“若需遍历则代价高”的场景
  for (let i = 0; i < 1000; i++) await a.lockRow('shop', 'orders', `R${i}`, 'X');
  assert.equal(lm.heldMode(a.id, table('shop', 'orders')), 'IX');

  const rowChecksBefore = lm.stats.conflictChecks.row;
  const tableChecksBefore = lm.stats.conflictChecks.table;

  // B 请求整表 X：由表条目上的 IX 与 X 互斥直接判定阻塞
  const bRequest = b.lockTable('shop', 'orders', 'X');
  assert.equal(await isBlocked(bRequest), true);
  assert.equal(lm.isWaiting(b.id), true);

  // 判定只发生在表级条目，未触碰任何行级条目
  assert.equal(lm.stats.conflictChecks.row, rowChecksBefore);
  assert.ok(lm.stats.conflictChecks.table > tableChecksBefore);

  // A 提交后，调度器唤醒 B
  a.commit();
  await bRequest;
  assert.equal(lm.heldMode(b.id, table('shop', 'orders')), 'X');
  b.commit();
});

test('表级 S 请求与行级 X 锁冲突（IS 与 S 相容但 IX 与 S 互斥）', async () => {
  const lm = new LockManager();
  const a = new Transaction(lm);
  const b = new Transaction(lm);
  await a.lockRow('shop', 'orders', 'R1', 'X');
  const bRequest = b.lockTable('shop', 'orders', 'S');
  assert.equal(await isBlocked(bRequest), true);
  a.commit();
  await bRequest;
  assert.equal(lm.heldMode(b.id, table('shop', 'orders')), 'S');
});

test('两事务对不同行加 X 锁可并行（IX 与 IX 相容）', async () => {
  const lm = new LockManager();
  const a = new Transaction(lm);
  const b = new Transaction(lm);
  await a.lockRow('shop', 'orders', 'R1', 'X');
  await b.lockRow('shop', 'orders', 'R2', 'X');
  assert.equal(lm.heldMode(a.id, row('shop', 'orders', 'R1')), 'X');
  assert.equal(lm.heldMode(b.id, row('shop', 'orders', 'R2')), 'X');
  assert.equal(lm.heldMode(a.id, table('shop', 'orders')), 'IX');
  assert.equal(lm.heldMode(b.id, table('shop', 'orders')), 'IX');
});

test('同一行上的 X 锁互斥，后者阻塞并在前者提交后获锁', async () => {
  const lm = new LockManager();
  const a = new Transaction(lm);
  const b = new Transaction(lm);
  await a.lockRow('shop', 'orders', 'R1', 'X');
  const bRequest = b.lockRow('shop', 'orders', 'R1', 'X');
  assert.equal(await isBlocked(bRequest), true);
  a.commit();
  await bRequest;
  assert.equal(lm.heldMode(b.id, row('shop', 'orders', 'R1')), 'X');
});

test('表级 S 与行级 S 相容（IS 不妨碍 S）', async () => {
  const lm = new LockManager();
  const a = new Transaction(lm);
  const b = new Transaction(lm);
  await a.lockRow('shop', 'orders', 'R1', 'S');
  await b.lockTable('shop', 'orders', 'S');
  assert.equal(lm.heldMode(b.id, table('shop', 'orders')), 'S');
});

test('锁升级：同事务先 S 后 IX 叠加为 SIX', async () => {
  const lm = new LockManager();
  const a = new Transaction(lm);
  await a.lockTable('shop', 'orders', 'S');
  await a.lockTable('shop', 'orders', 'IX');
  assert.equal(lm.heldMode(a.id, table('shop', 'orders')), 'SIX');
});

test('FIFO 公平调度：等待者按序唤醒', async () => {
  const lm = new LockManager();
  const a = new Transaction(lm);
  const b = new Transaction(lm);
  const c = new Transaction(lm);
  await a.lockRow('shop', 'orders', 'R1', 'X');
  const order: number[] = [];
  const bReq = b.lockRow('shop', 'orders', 'R1', 'X').then(() => void order.push(b.id));
  const cReq = c.lockRow('shop', 'orders', 'R1', 'S').then(() => void order.push(c.id));
  assert.equal(await isBlocked(Promise.all([bReq, cReq]).then(() => {})), true);
  a.commit();
  await bReq;
  assert.deepEqual(order, [b.id]);
  b.commit();
  await cReq;
  assert.deepEqual(order, [b.id, c.id]);
});

test('死锁检测：相互等待时抛出 DeadlockError', async () => {
  const lm = new LockManager();
  const a = new Transaction(lm);
  const b = new Transaction(lm);
  await a.lockRow('shop', 'orders', 'R1', 'X');
  await b.lockRow('shop', 'orders', 'R2', 'X');
  const aWaits = a.lockRow('shop', 'orders', 'R2', 'X');
  await assert.rejects(b.lockRow('shop', 'orders', 'R1', 'X'), DeadlockError);
  // b 的死锁请求被中止后，a 仍需等待 b 释放 R2
  assert.equal(await isBlocked(aWaits), true);
  b.abort();
  await aWaits;
});

test('提交释放全部层级锁，空条目被回收', async () => {
  const lm = new LockManager();
  const a = new Transaction(lm);
  await a.lockRow('shop', 'orders', 'R1', 'X');
  a.commit();
  assert.deepEqual(lm.snapshot(), []);
});
