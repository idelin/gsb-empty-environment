import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  OccStore,
  OccRetryExhaustedError,
  runOptimistic,
} from '../src/occ.ts';
import { LockStore } from '../src/lock.ts';

const tick = (): Promise<void> => new Promise((r) => setImmediate(r));

test('读取阶段：read 记录 ReadSet 版本，write 仅缓冲到 WriteSet', () => {
  const store = new OccStore();
  const setup = store.begin();
  setup.write('a', 1);
  assert.equal(setup.commit(), true);

  const tx = store.begin();
  assert.equal(tx.read<number>('a'), 1);
  tx.write('b', 2);

  assert.deepEqual([...tx.readSet.keys()], ['a']);
  assert.deepEqual([...tx.writeSet.keys()], ['b']);
  // 写入仍停留在缓冲区，存储不可见
  assert.equal(store.peek('b').value, undefined);
});

test('写入阶段：验证通过后 WriteSet 一次性批量提交', () => {
  const store = new OccStore();
  const tx = store.begin();
  tx.write('x', 10);
  tx.write('y', 20);
  tx.write('z', 30);
  assert.equal(tx.commit(), true);

  assert.equal(store.peek('x').value, 10);
  assert.equal(store.peek('y').value, 20);
  assert.equal(store.peek('z').value, 30);
  // 三个键共享同一个提交版本 —— 批量原子提交
  const versions = new Set(['x', 'y', 'z'].map((k) => store.versionOf(k)));
  assert.equal(versions.size, 1);
});

test('验证阶段：ReadSet 版本被其他已提交事务修改时精准检出并干净回滚', () => {
  const store = new OccStore();
  const seed = store.begin();
  seed.write('hot', 0);
  seed.write('other', 0);
  assert.equal(seed.commit(), true);

  const txA = store.begin();
  assert.equal(txA.read<number>('hot'), 0); // ReadSet: hot@v1
  txA.write('other', 100); // 只缓冲，未落库

  // 并发事务修改 hot 并提交
  const txB = store.begin();
  txB.write('hot', 999);
  assert.equal(txB.commit(), true);

  // txA 提交时验证发现 hot 版本过期 -> 中止
  assert.equal(txA.commit(), false);
  assert.equal(txA.status, 'aborted');

  // 干净回滚：txA 缓冲的写绝不能渗入存储
  assert.equal(store.peek('other').value, 0);
  assert.equal(store.peek('hot').value, 999);
  assert.equal(store.stats.aborts, 1);
});

test('验证阶段无假阳性：ReadSet 之外的键被修改不影响提交', () => {
  const store = new OccStore();
  const seed = store.begin();
  seed.write('k1', 1);
  seed.write('k2', 2);
  assert.equal(seed.commit(), true);

  const txA = store.begin();
  txA.read('k1'); // 只读 k1

  const txB = store.begin();
  txB.write('k2', 22); // 修改的是 txA 未读的键
  assert.equal(txB.commit(), true);

  txA.write('k1', 11);
  assert.equal(txA.commit(), true);
  assert.equal(store.peek('k1').value, 11);
  assert.equal(store.peek('k2').value, 22);
});

test('read-your-own-writes：读取阶段能读到自己缓冲的写', () => {
  const store = new OccStore();
  const tx = store.begin();
  tx.write('n', 5);
  assert.equal(tx.read<number>('n'), 5);
  // 自己的写不进入 ReadSet（无需自我验证）
  assert.equal(tx.readSet.has('n'), false);
  assert.equal(tx.commit(), true);
});

test('重叠写入：并发增量只有一个先提交，另一个验证失败并干净回滚', () => {
  const store = new OccStore();
  const seed = store.begin();
  seed.write('counter', 0);
  assert.equal(seed.commit(), true);

  const txA = store.begin();
  const txB = store.begin();
  const a = txA.read<number>('counter')!;
  const b = txB.read<number>('counter')!;
  txA.write('counter', a + 1);
  txB.write('counter', b + 1);

  assert.equal(txA.commit(), true);
  assert.equal(txB.commit(), false); // ReadSet 版本过期
  assert.equal(txB.status, 'aborted');
  // 回滚干净：没有被覆盖写
  assert.equal(store.peek('counter').value, 1);
});

test('高并发热点键：无丢失更新，冲突率被统计', async () => {
  const store = new OccStore();
  const seed = store.begin();
  for (let i = 0; i < 8; i++) seed.write(`hot:${i}`, 0);
  assert.equal(seed.commit(), true);
  store.resetStats();

  const WORKERS = 64;
  const INCREMENTS_PER_WORKER = 20;

  await Promise.all(
    Array.from({ length: WORKERS }, async (_, w) => {
      for (let i = 0; i < INCREMENTS_PER_WORKER; i++) {
        const key = `hot:${(w + i) % 8}`; // 8 个热点键
        await runOptimistic(store, (tx) => {
          const v = tx.read<number>(key)!;
          tx.write(key, v + 1);
        });
        if (i % 3 === 0) await tick(); // 交错让出，制造并发冲突
      }
    }),
  );

  // 全部重试成功：无丢失更新
  let total = 0;
  for (let i = 0; i < 8; i++) total += store.peek(`hot:${i}`).value as number;
  assert.equal(total, WORKERS * INCREMENTS_PER_WORKER);

  const stats = store.stats;
  assert.equal(stats.commits, WORKERS * INCREMENTS_PER_WORKER);
  assert.ok(stats.aborts > 0, '热点键高并发下应产生冲突');
  assert.ok(stats.conflictRate > 0 && stats.conflictRate < 1);
  console.log(
    `    [hot-key] commits=${stats.commits} aborts=${stats.aborts} ` +
      `conflictRate=${(stats.conflictRate * 100).toFixed(1)}%`,
  );
});

test('读多写少场景：OCC 吞吐大幅领先锁方案', async () => {
  const KEYSPACE = 32;
  const TXNS = 300;

  // —— OCC ——
  const occ = new OccStore();
  {
    const seed = occ.begin();
    for (let k = 0; k < KEYSPACE; k++) seed.write(`k${k}`, 0);
    seed.commit();
    occ.resetStats();
  }
  const occStart = performance.now();
  await Promise.all(
    Array.from({ length: TXNS }, (_, i) =>
      runOptimistic(occ, async (tx) => {
        await tick(); // 模拟业务延迟：OCC 下各事务并发推进
        const k = i % KEYSPACE;
        const v = tx.read<number>(`k${k}`)!;
        if (i % 10 === 0) tx.write(`k${k}`, v + 1); // 10% 写
      }),
    ),
  );
  const occMs = performance.now() - occStart;

  // —— 锁方案 ——
  const lock = new LockStore();
  for (let k = 0; k < KEYSPACE; k++) lock.write(`k${k}`, 0);
  const lockStart = performance.now();
  await Promise.all(
    Array.from({ length: TXNS }, (_, i) =>
      lock.run(async (store) => {
        await tick(); // 持锁期间的延迟会阻塞所有其他事务
        const k = i % KEYSPACE;
        const v = store.read<number>(`k${k}`)!;
        if (i % 10 === 0) store.write(`k${k}`, v + 1);
      }),
    ),
  );
  const lockMs = performance.now() - lockStart;

  const occTps = TXNS / (occMs / 1000);
  const lockTps = TXNS / (lockMs / 1000);
  console.log(
    `    [read-heavy] OCC ${occTps.toFixed(0)} tx/s vs Lock ${lockTps.toFixed(0)} tx/s ` +
      `(speedup x${(occTps / lockTps).toFixed(1)}, occMs=${occMs.toFixed(1)} lockMs=${lockMs.toFixed(1)})`,
  );

  assert.ok(
    occTps > lockTps * 2,
    `OCC 吞吐 (${occTps.toFixed(0)}) 应大幅领先锁方案 (${lockTps.toFixed(0)})`,
  );
});

test('自旋重试执行器：冲突后自动重试并最终成功', async () => {
  const store = new OccStore();
  const seed = store.begin();
  seed.write('c', 0);
  assert.equal(seed.commit(), true);

  // 后台事务不断制造冲突
  let stop = false;
  const interferer = (async () => {
    while (!stop) {
      const tx = store.begin();
      tx.write('c', (store.peek('c').value as number) + 1);
      tx.commit();
      await tick();
    }
  })();

  let attempts = 0;
  const result = await runOptimistic(
    store,
    (tx) => {
      attempts++;
      const v = tx.read<number>('c')!;
      tx.write('c', v + 1);
      return v;
    },
    { spinBudget: 2 },
  );
  stop = true;
  await interferer;

  assert.ok(typeof result === 'number');
  assert.ok(attempts >= 1);
});

test('重试耗尽：达到 maxRetries 后抛出 OccRetryExhaustedError', async () => {
  const store = new OccStore();
  const seed = store.begin();
  seed.write('c', 0);
  assert.equal(seed.commit(), true);

  await assert.rejects(
    runOptimistic(
      store,
      (tx) => {
        tx.read('c');
        // 事务体内部制造版本过期，确保永远冲突
        const other = store.begin();
        other.write('c', Math.random());
        other.commit();
        tx.write('c', 1);
      },
      { maxRetries: 3 },
    ),
    (err: unknown) => {
      assert.ok(err instanceof OccRetryExhaustedError);
      assert.equal((err as OccRetryExhaustedError).attempts, 4);
      return true;
    },
  );
});

test('已结束事务拒绝继续操作', () => {
  const store = new OccStore();
  const tx = store.begin();
  tx.write('a', 1);
  assert.equal(tx.commit(), true);
  assert.throws(() => tx.read('a'), /committed/);
  assert.throws(() => tx.write('a', 2), /committed/);
});
