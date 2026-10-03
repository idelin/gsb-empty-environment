import { OccEngine } from '../src/occ/engine.ts';
import type { OccStatsSnapshot } from '../src/occ/engine.ts';
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

interface WorkloadOptions {
  workers: number;
  txnsPerWorker: number;
  keySpace: number;
  hotKeys: number;
  writeRatio: number;
  readsPerTxn: number;
  ioMs: number;
  seed: number;
}

interface KvTxn {
  get(key: string): number | undefined;
  set(key: string, value: number): void;
}

interface RunResult {
  ms: number;
  txns: number;
}

async function runWorkload(
  opts: WorkloadOptions,
  runTxn: (fn: (txn: KvTxn) => Promise<void>) => Promise<unknown>,
): Promise<RunResult> {
  const rng = mulberry32(opts.seed);
  const start = performance.now();
  await Promise.all(
    Array.from({ length: opts.workers }, async () => {
      for (let i = 0; i < opts.txnsPerWorker; i += 1) {
        // 在事务体外确定本次事务的逻辑计划，保证 OCC 重试时重放同一逻辑操作，
        // 且两种引擎执行完全相同的负载。
        const isWrite = rng() < opts.writeRatio;
        const writeKey = `k${Math.floor(rng() * opts.hotKeys)}`;
        const readKeys = Array.from(
          { length: opts.readsPerTxn },
          () => `k${Math.floor(rng() * opts.keySpace)}`,
        );
        await runTxn(async (txn) => {
          if (isWrite) {
            const v = txn.get(writeKey) ?? 0;
            await sleep(opts.ioMs);
            txn.set(writeKey, v + 1);
          } else {
            for (const key of readKeys) {
              txn.get(key);
              await sleep(opts.ioMs);
            }
          }
        });
      }
    }),
  );
  return { ms: performance.now() - start, txns: opts.workers * opts.txnsPerWorker };
}

function report(name: string, occ: RunResult, lock: RunResult, stats: OccStatsSnapshot<string>): void {
  const occTps = (occ.txns / occ.ms) * 1000;
  const lockTps = (lock.txns / lock.ms) * 1000;
  console.log(`\n### ${name}`);
  console.log(
    `OCC  : ${occ.ms.toFixed(1).padStart(8)} ms | ${occTps.toFixed(0).padStart(7)} txn/s | ` +
      `尝试=${stats.attempts} 提交=${stats.commits} 中止=${stats.aborts} ` +
      `冲突率=${(stats.conflictRate * 100).toFixed(1)}%`,
  );
  console.log(`LOCK : ${lock.ms.toFixed(1).padStart(8)} ms | ${lockTps.toFixed(0).padStart(7)} txn/s | 串行执行，无冲突概念`);
  console.log(`吞吐加速比: ${(occTps / lockTps).toFixed(1)}x`);
}

async function scenario(
  name: string,
  opts: WorkloadOptions,
  verify: (occ: OccEngine<string, number>, lock: LockEngine<string, number>) => void,
): Promise<void> {
  const occ = new OccEngine<string, number>({ maxRetries: 500 });
  const lock = new LockEngine<string, number>();
  await occ.run((txn) => {
    for (let i = 0; i < opts.keySpace; i += 1) txn.set(`k${i}`, 0);
  });
  await lock.run((txn) => {
    for (let i = 0; i < opts.keySpace; i += 1) txn.set(`k${i}`, 0);
  });
  occ.resetStats();

  const occResult = await runWorkload(opts, (fn) => occ.run(fn));
  const lockResult = await runWorkload(opts, (fn) => lock.run(fn));
  verify(occ, lock);
  report(name, occResult, lockResult, occ.getStats());
}

console.log('OCC vs 全局锁基线（每个操作模拟 1ms I/O 延迟）');

await scenario(
  '场景 A：读多写少（90% 读 / 10% 写，32 并发，128 键，8 个热点键）',
  { workers: 32, txnsPerWorker: 6, keySpace: 128, hotKeys: 8, writeRatio: 0.1, readsPerTxn: 4, ioMs: 1, seed: 42 },
  (occ, lock) => {
    for (let i = 0; i < 8; i += 1) {
      const a = occ.store.getCommitted(`k${i}`)?.value;
      const b = lock.getCommitted(`k${i}`);
      if (a !== b) throw new Error(`数据不一致: k${i} occ=${a} lock=${b}`);
    }
    console.log('数据一致性校验通过（OCC 与锁方案结果一致）');
  },
);

await scenario(
  '场景 B：写热点（100% 写同一热点键，48 并发）',
  { workers: 48, txnsPerWorker: 2, keySpace: 1, hotKeys: 1, writeRatio: 1, readsPerTxn: 0, ioMs: 1, seed: 7 },
  (occ, lock) => {
    const expected = 48 * 2;
    const a = occ.store.getCommitted('k0')?.value;
    const b = lock.getCommitted('k0');
    if (a !== expected || b !== expected) {
      throw new Error(`计数错误: occ=${a} lock=${b} expected=${expected}`);
    }
    console.log(`数据一致性校验通过（counter=${a}，无丢失更新）`);
  },
);
