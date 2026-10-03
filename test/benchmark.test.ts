import { test } from 'node:test';
import assert from 'node:assert/strict';
import { OccEngine } from '../src/occ/engine.ts';
import { LockEngine } from '../src/lock/lock-engine.ts';
import { sleep } from '../src/util/sleep.ts';

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface KvTxn {
  get(key: string): number | undefined;
  set(key: string, value: number): void;
}

test('读多写少场景：OCC 吞吐大幅领先锁方案', async () => {
  const workers = 24;
  const txnsPerWorker = 4;
  const keySpace = 64;
  const hotKeys = 8;
  const ioMs = 1;

  const occ = new OccEngine<string, number>({ maxRetries: 200 });
  const lock = new LockEngine<string, number>();
  await occ.run((txn) => {
    for (let i = 0; i < keySpace; i += 1) txn.set(`k${i}`, 0);
  });
  await lock.run((txn) => {
    for (let i = 0; i < keySpace; i += 1) txn.set(`k${i}`, 0);
  });
  occ.resetStats();

  // 相同种子 -> 两种引擎执行完全相同的负载：90% 只读事务 + 10% 热点键自增
  const runAll = async (runTxn: (fn: (txn: KvTxn) => Promise<void>) => Promise<unknown>) => {
    const rng = mulberry32(42);
    const start = performance.now();
    await Promise.all(
      Array.from({ length: workers }, async () => {
        for (let i = 0; i < txnsPerWorker; i += 1) {
          // 事务计划在体外生成：OCC 重试时重放同一逻辑操作
          const isWrite = rng() < 0.1;
          const writeKey = `k${Math.floor(rng() * hotKeys)}`;
          const readKeys = Array.from({ length: 4 }, () => `k${Math.floor(rng() * keySpace)}`);
          await runTxn(async (txn) => {
            if (isWrite) {
              const v = txn.get(writeKey) ?? 0;
              await sleep(ioMs);
              txn.set(writeKey, v + 1);
            } else {
              for (const key of readKeys) {
                txn.get(key);
                await sleep(ioMs);
              }
            }
          });
        }
      }),
    );
    return performance.now() - start;
  };

  const occMs = await runAll((fn) => occ.run(fn));
  const lockMs = await runAll((fn) => lock.run(fn));
  const stats = occ.getStats();
  const totalTxns = workers * txnsPerWorker;

  console.log(
    `  [读多写少] OCC=${occMs.toFixed(1)}ms (${((totalTxns / occMs) * 1000).toFixed(0)} txn/s) ` +
      `vs LOCK=${lockMs.toFixed(1)}ms (${((totalTxns / lockMs) * 1000).toFixed(0)} txn/s), ` +
      `加速比=${(lockMs / occMs).toFixed(1)}x, OCC冲突率=${(stats.conflictRate * 100).toFixed(1)}%`,
  );
  assert.equal(stats.commits, totalTxns);
  assert.ok(occMs * 2 < lockMs, `OCC 应至少快 2 倍: occ=${occMs}ms lock=${lockMs}ms`);
});
