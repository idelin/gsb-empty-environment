/**
 * demo.ts — OCC 引擎运行样例
 *
 * 场景 1：高并发热点键读写 + 冲突率统计
 * 场景 2：读多写少负载下 OCC vs 锁方案吞吐对比
 * 场景 3：重叠写入 -> 精准验证出版本过期 -> 干净回滚
 */
import { OccStore, runOptimistic } from '../src/occ.ts';
import { LockStore } from '../src/lock.ts';

const tick = (): Promise<void> => new Promise((r) => setImmediate(r));

async function scenarioHotKeys(): Promise<void> {
  console.log('=== 场景 1：高并发热点键读写（64 工作线程 x 25 次增量，8 个热点键）===');
  const store = new OccStore();
  const seed = store.begin();
  for (let i = 0; i < 8; i++) seed.write(`hot:${i}`, 0);
  seed.commit();
  store.resetStats();

  const WORKERS = 64;
  const OPS = 25;
  const start = performance.now();
  await Promise.all(
    Array.from({ length: WORKERS }, async (_, w) => {
      for (let i = 0; i < OPS; i++) {
        const key = `hot:${(w * 7 + i) % 8}`;
        await runOptimistic(store, async (tx) => {
          const v = tx.read<number>(key)!;
          await tick(); // 读取与提交之间让出事件循环，形成真实冲突窗口
          tx.write(key, v + 1);
        });
        if (i % 4 === 0) await tick();
      }
    }),
  );
  const ms = performance.now() - start;

  let total = 0;
  for (let i = 0; i < 8; i++) total += store.peek(`hot:${i}`).value as number;
  const s = store.stats;
  console.log(`  提交事务: ${s.commits}  中止事务: ${s.aborts}  冲突率: ${(s.conflictRate * 100).toFixed(1)}%`);
  console.log(`  计数器总和: ${total}（期望 ${WORKERS * OPS}，无丢失更新）`);
  console.log(`  吞吐: ${(s.commits / (ms / 1000)).toFixed(0)} tx/s\n`);
}

async function scenarioReadHeavy(): Promise<void> {
  console.log('=== 场景 2：读多写少（90% 读 / 10% 写，300 并发事务）OCC vs 锁 ===');
  const KEYSPACE = 32;
  const TXNS = 300;

  const occ = new OccStore();
  {
    const seed = occ.begin();
    for (let k = 0; k < KEYSPACE; k++) seed.write(`k${k}`, 0);
    seed.commit();
    occ.resetStats();
  }
  let start = performance.now();
  await Promise.all(
    Array.from({ length: TXNS }, (_, i) =>
      runOptimistic(occ, async (tx) => {
        await tick();
        const k = i % KEYSPACE;
        const v = tx.read<number>(`k${k}`)!;
        if (i % 10 === 0) tx.write(`k${k}`, v + 1);
      }),
    ),
  );
  const occMs = performance.now() - start;

  const lock = new LockStore();
  for (let k = 0; k < KEYSPACE; k++) lock.write(`k${k}`, 0);
  start = performance.now();
  await Promise.all(
    Array.from({ length: TXNS }, (_, i) =>
      lock.run(async (store) => {
        await tick();
        const k = i % KEYSPACE;
        const v = store.read<number>(`k${k}`)!;
        if (i % 10 === 0) store.write(`k${k}`, v + 1);
      }),
    ),
  );
  const lockMs = performance.now() - start;

  const occTps = TXNS / (occMs / 1000);
  const lockTps = TXNS / (lockMs / 1000);
  console.log(`  OCC : ${occMs.toFixed(1)} ms, ${occTps.toFixed(0)} tx/s, 冲突率 ${(occ.stats.conflictRate * 100).toFixed(1)}%`);
  console.log(`  锁  : ${lockMs.toFixed(1)} ms, ${lockTps.toFixed(0)} tx/s, 等锁次数 ${lock.stats.waits}`);
  console.log(`  OCC 加速比: x${(occTps / lockTps).toFixed(1)}\n`);
}

function scenarioOverlapRollback(): void {
  console.log('=== 场景 3：重叠写入 -> 版本过期验证 -> 干净回滚 ===');
  const store = new OccStore();
  const seed = store.begin();
  seed.write('balance', 100);
  seed.commit();

  const alice = store.begin();
  const bob = store.begin();
  const aBal = alice.read<number>('balance')!;
  const bBal = bob.read<number>('balance')!;
  alice.write('balance', aBal - 30); // Alice 扣款
  bob.write('balance', bBal - 50);   // Bob 同时扣款（基于同一快照）

  console.log(`  Alice 提交: ${alice.commit()}  -> balance = ${store.peek('balance').value}`);
  const bobOk = bob.commit();
  console.log(`  Bob   提交: ${bobOk}（ReadSet 中 balance@v1 已被 Alice 的提交改为 v2）`);
  console.log(`  Bob 状态: ${bob.status}，回滚后 balance 仍为 ${store.peek('balance').value}（未被覆盖写）`);
}

await scenarioHotKeys();
await scenarioReadHeavy();
scenarioOverlapRollback();
