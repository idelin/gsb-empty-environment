import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ConflictArbiter, WoundError } from '../src/concurrency/arbiter.ts';

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

test('基本授予与释放，结束后无泄漏', async () => {
  const arb = new ConflictArbiter();
  const n1 = arb.registerNode();
  const h = await arb.acquire(n1, 'K', 'EXCLUSIVE');
  assert.equal(arb.heldLockCount(), 1);
  arb.release(h);
  assert.equal(arb.heldLockCount(), 0);
  arb.assertQuiescent();
});

test('共享锁兼容：多节点同时持有', async () => {
  const arb = new ConflictArbiter();
  const n1 = arb.registerNode();
  const n2 = arb.registerNode();
  const h1 = await arb.acquire(n1, 'K', 'SHARED');
  const h2 = await arb.acquire(n2, 'K', 'SHARED');
  assert.equal(arb.heldLockCount(), 2);
  arb.release(h1);
  arb.release(h2);
  arb.assertQuiescent();
});

test('排他冲突：后者 FIFO 等待，前者释放后按序授予', async () => {
  const arb = new ConflictArbiter();
  const n1 = arb.registerNode();
  const n2 = arb.registerNode();
  const n3 = arb.registerNode();
  const h1 = await arb.acquire(n1, 'K', 'EXCLUSIVE');
  const order: number[] = [];
  const p2 = arb.acquire(n2, 'K', 'EXCLUSIVE').then((h) => (order.push(2), h));
  const p3 = arb.acquire(n3, 'K', 'EXCLUSIVE').then((h) => (order.push(3), h));
  await sleep(10);
  assert.equal(arb.pendingWaiterCount(), 2);
  arb.release(h1);
  const h2 = await p2;
  await sleep(10);
  arb.release(h2);
  const h3 = await p3;
  arb.release(h3);
  assert.deepEqual(order, [2, 3]); // FIFO 顺序
  arb.assertQuiescent();
});

test('共享-排他冲突：排他请求等待全部共享持有者释放', async () => {
  const arb = new ConflictArbiter();
  const n1 = arb.registerNode();
  const n2 = arb.registerNode();
  const n3 = arb.registerNode();
  const s1 = await arb.acquire(n1, 'K', 'SHARED');
  const s2 = await arb.acquire(n2, 'K', 'SHARED');
  let granted = false;
  const px = arb.acquire(n3, 'K', 'EXCLUSIVE').then((h) => ((granted = true), h));
  await sleep(10);
  assert.equal(granted, false);
  arb.release(s1);
  await sleep(10);
  assert.equal(granted, false); // 仍有共享持有者
  arb.release(s2);
  const hx = await px;
  assert.equal(granted, true);
  arb.release(hx);
  arb.assertQuiescent();
});

test('wound-wait：年长者击伤年轻持有者，年轻者提交被拦截', async () => {
  const arb = new ConflictArbiter();
  const older = arb.registerNode(); // 时间戳更小 = 年长
  const younger = arb.registerNode();
  const wounded: number[] = [];
  arb.onWound = (id) => wounded.push(id);

  // 年轻者先拿锁并开始一个“慢”事务
  const youngTxn = arb.transact(younger, 'K', async () => {
    await sleep(50); // 持有锁期间被年长者击伤
    return 'young';
  });
  await sleep(10);
  // 年长者到达：击伤年轻者，自己获得锁并提交
  const oldResult = await arb.transact(older, 'K', () => 'old');
  assert.equal(oldResult, 'old');
  await assert.rejects(youngTxn, WoundError);
  assert.deepEqual(wounded, [younger]);
  // 年轻者的修改被丢弃，寄存器是年长者的值
  assert.equal(arb.readRegister('K'), 'old');
  arb.assertQuiescent();
});

test('wound-wait：年轻者不击伤年长者，只能等待', async () => {
  const arb = new ConflictArbiter();
  const older = arb.registerNode();
  const younger = arb.registerNode();
  const h = await arb.acquire(older, 'K', 'EXCLUSIVE');
  const p = arb.acquire(younger, 'K', 'EXCLUSIVE');
  await sleep(10);
  assert.equal(arb.events.filter((e) => e.kind === 'WOUND').length, 0);
  assert.equal(arb.pendingWaiterCount(), 1);
  arb.release(h);
  const hy = await p;
  arb.release(hy);
  arb.assertQuiescent();
});

test('无死锁：互相持锁并交叉请求，wound-wait 必然打破循环等待', async () => {
  const arb = new ConflictArbiter();
  const n1 = arb.registerNode(); // 年长
  const n2 = arb.registerNode(); // 年轻

  const hA1 = await arb.acquire(n1, 'A', 'EXCLUSIVE');
  const hB2 = await arb.acquire(n2, 'B', 'EXCLUSIVE');

  // n1 想要 B（被年轻的 n2 持有）→ 击伤 n2；n2 对 A 的等待请求被中止
  const n2WantA = arb.acquire(n2, 'A', 'EXCLUSIVE');
  const n1WantB = arb.acquire(n1, 'B', 'EXCLUSIVE');
  const [r1, r2] = await Promise.allSettled([n1WantB, n2WantA]);
  assert.equal(r1.status, 'fulfilled'); // 年长者获胜
  assert.equal(r2.status, 'rejected'); // 年轻者被击伤
  assert.ok((r2 as PromiseRejectedResult).reason instanceof WoundError);

  arb.release(hA1);
  arb.release(r1.value as Awaited<typeof n1WantB>);
  arb.assertQuiescent(); // 无残留等待 => 无死锁
});

test('等待中的节点被击伤：中止传播到其排队请求', async () => {
  const arb = new ConflictArbiter();
  const n1 = arb.registerNode(); // 最年长
  const n2 = arb.registerNode();
  // n1 持 K1；n2 持 K2 并排队等 K1
  const h1 = await arb.acquire(n1, 'K1', 'EXCLUSIVE');
  const h2 = await arb.acquire(n2, 'K2', 'EXCLUSIVE');
  const waiting = arb.acquire(n2, 'K1', 'EXCLUSIVE');
  await sleep(10);
  assert.equal(arb.pendingWaiterCount(), 1);
  // n1 请求 K2 → 击伤 n2 → n2 的排队请求被 reject
  const hk2 = await arb.acquire(n1, 'K2', 'EXCLUSIVE');
  await assert.rejects(waiting, WoundError);
  assert.equal(arb.pendingWaiterCount(), 0);
  arb.release(h1);
  arb.release(hk2);
  arb.assertQuiescent();
});

test('锁升级：独占持有 SHARED 时可原地升级为 EXCLUSIVE', async () => {
  const arb = new ConflictArbiter();
  const n1 = arb.registerNode();
  const hs = await arb.acquire(n1, 'K', 'SHARED');
  const hx = await arb.acquire(n1, 'K', 'EXCLUSIVE'); // 无其他持有者 → 立即升级
  arb.release(hx);
  arb.release(hs);
  arb.assertQuiescent();
});

test('双方同时升级形成循环：wound-wait 仲裁，年轻者被击伤', async () => {
  const arb = new ConflictArbiter();
  const older = arb.registerNode();
  const younger = arb.registerNode();
  const so = await arb.acquire(older, 'K', 'SHARED');
  const sy = await arb.acquire(younger, 'K', 'SHARED');
  // 年轻者先请求升级：因年长者持有 SHARED 而进入等待（仍持有自己的 SHARED）
  const upYoung = arb.acquire(younger, 'K', 'EXCLUSIVE');
  // 年长者随后请求升级：击伤年轻者（剥夺其 SHARED 并中止其等待），立即获得升级
  const upOld = arb.acquire(older, 'K', 'EXCLUSIVE');
  const [ro, ry] = await Promise.allSettled([upOld, upYoung]);
  assert.equal(ro.status, 'fulfilled');
  assert.equal(ry.status, 'rejected');
  assert.ok((ry as PromiseRejectedResult).reason instanceof WoundError);
  arb.release(so);
  arb.release(ro.value as Awaited<typeof upOld>);
  arb.assertQuiescent();
});

test('重入与引用计数：同级重复申请需成对释放', async () => {
  const arb = new ConflictArbiter();
  const n1 = arb.registerNode();
  const n2 = arb.registerNode();
  const h1 = await arb.acquire(n1, 'K', 'EXCLUSIVE');
  const h2 = await arb.acquire(n1, 'K', 'EXCLUSIVE'); // 重入
  const waiting = arb.acquire(n2, 'K', 'EXCLUSIVE');
  await sleep(10);
  arb.release(h1);
  await sleep(10);
  assert.equal(arb.pendingWaiterCount(), 1); // 引用计数未归零，锁未释放
  arb.release(h2);
  const hn2 = await waiting;
  arb.release(hn2);
  arb.assertQuiescent();
});

test('重复释放抛错，防止句柄泄漏误用', async () => {
  const arb = new ConflictArbiter();
  const n1 = arb.registerNode();
  const h = await arb.acquire(n1, 'K', 'EXCLUSIVE');
  arb.release(h);
  assert.throws(() => arb.release(h), /double release/);
  arb.assertQuiescent();
});

test('线性一致性：多节点高并发竞争同一计数器，无丢失更新、无泄漏', async () => {
  const arb = new ConflictArbiter();
  const NODE_COUNT = 8;
  const OPS_PER_NODE = 50;
  const nodes = Array.from({ length: NODE_COUNT }, () => arb.registerNode());

  await Promise.all(
    nodes.map((nodeId) =>
      (async () => {
        for (let i = 0; i < OPS_PER_NODE; i++) {
          await arb.transactWithRetry<number>(nodeId, 'counter', async (cur) => {
            await sleep(Math.random() < 0.3 ? 1 : 0); // 制造交错
          return (cur ?? 0) + 1;
          });
        }
      })(),
    ),
  );

  // 原子性：全部 400 次递增生效，无丢失更新
  assert.equal(arb.readRegister<number>('counter'), NODE_COUNT * OPS_PER_NODE);

  // 线性一致性：COMMIT 事件数量正确且全局序号严格递增
  const commits = arb.events.filter((e) => e.kind === 'COMMIT');
  assert.equal(commits.length, NODE_COUNT * OPS_PER_NODE);
  const seqs = arb.events.map((e) => e.seq);
  for (let i = 1; i < seqs.length; i++) assert.ok(seqs[i] > seqs[i - 1]);

  // 严格无泄漏
  assert.equal(arb.heldLockCount(), 0);
  assert.equal(arb.pendingWaiterCount(), 0);
  arb.assertQuiescent();
});

test('多键并行：不相交键上的操作互不阻塞', async () => {
  const arb = new ConflictArbiter();
  const nodes = Array.from({ length: 4 }, () => arb.registerNode());
  const results = await Promise.all(
    nodes.map((n, i) => arb.transact(n, `key-${i}`, () => i * 10)),
  );
  assert.deepEqual(results, [0, 10, 20, 30]);
  // 全部立即授予，无任何 WAIT 事件
  assert.equal(arb.events.filter((e) => e.kind === 'WAIT').length, 0);
  arb.assertQuiescent();
});

test('被击伤节点重试后最终成功（复用 nodeId 保持时间戳）', async () => {
  const arb = new ConflictArbiter();
  const older = arb.registerNode();
  const younger = arb.registerNode();
  // 年轻者慢事务被击伤 → transactWithRetry 自动重试成功
  const youngTxn = arb.transactWithRetry(younger, 'K', async (cur) => {
    await sleep(30);
    return (cur ?? 0) + 1;
  });
  await sleep(5);
  await arb.transact(older, 'K', (cur) => (cur ?? 0) + 1);
  const final = await youngTxn;
  assert.equal(final, 2);
  assert.equal(arb.readRegister('K'), 2);
  arb.assertQuiescent();
});
