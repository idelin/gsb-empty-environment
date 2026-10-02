/**
 * 验收边界测试：
 * - 1 个写线程持续密集更新 1000 个键（转账事务，总和守恒）；
 * - 5 个并发读线程持续执行全表扫描校验和统计；
 * - 写操作无等待，读操作每次快照内部总和守恒，验证零撕裂。
 *
 * 写/读线程均为真实 OS 线程（worker_threads），共享同一块
 * SharedArrayBuffer，读路径全程无锁。
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import { Worker } from 'node:worker_threads';
import { MvccStore } from '../src/store.ts';

const KEY_COUNT = 1000;
const INITIAL_VALUE = 1000;
const EXPECTED_SUM = KEY_COUNT * INITIAL_VALUE;
const READER_COUNT = 5;
const COMMITS = Number(process.env.STRESS_COMMITS ?? 400_000);

interface WorkerStats {
  commits?: number;
  scans?: number;
  torn?: number;
}

function runWorker(script: string, workerData: unknown): Promise<WorkerStats> {
  return new Promise((resolve, reject) => {
    const worker = new Worker(new URL(script, import.meta.url), { workerData });
    worker.once('message', resolve);
    worker.once('error', reject);
    worker.once('exit', (code) => {
      if (code !== 0) reject(new Error(`worker exited with code ${code}`));
    });
  });
}

test(
  `验收：1 写线程 × ${READER_COUNT} 读线程 × ${KEY_COUNT} 键，零撕裂`,
  { timeout: 180_000 },
  async () => {
    const poolCapacity = KEY_COUNT + COMMITS * 2 + 16;
    const store = MvccStore.create(KEY_COUNT, poolCapacity);
    store.initAll(INITIAL_VALUE);

    const startedAt = performance.now();

    const writer = runWorker('../src/workers/writer.worker.ts', {
      sab: store.sab,
      keyCount: KEY_COUNT,
      poolCapacity,
      commits: COMMITS,
      initialValue: INITIAL_VALUE,
      seed: 42,
    });
    const readers = Array.from({ length: READER_COUNT }, () =>
      runWorker('../src/workers/reader.worker.ts', {
        sab: store.sab,
        keyCount: KEY_COUNT,
        poolCapacity,
        expectedSum: EXPECTED_SUM,
      }),
    );

    const [writerStats, ...readerStats] = await Promise.all([writer, ...readers]);
    const elapsedMs = performance.now() - startedAt;

    const totalScans = readerStats.reduce((acc, s) => acc + (s.scans ?? 0), 0);
    const totalTorn = readerStats.reduce((acc, s) => acc + (s.torn ?? 0), 0);

    console.log(
      `    写事务=${writerStats.commits} (${Math.round(COMMITS / (elapsedMs / 1000)).toLocaleString()} tx/s), ` +
        `读扫描=${totalScans.toLocaleString()} (${Math.round(totalScans / (elapsedMs / 1000)).toLocaleString()} scan/s), ` +
        `撕裂=${totalTorn}, 耗时=${elapsedMs.toFixed(0)}ms`,
    );

    // 写线程完成全部提交，期间无任何等待。
    assert.equal(writerStats.commits, COMMITS);
    // 读线程确实在写线程运行期间持续扫描。
    assert.ok(totalScans > READER_COUNT, `扫描次数过少: ${totalScans}`);
    // 核心验收：零撕裂 —— 每次快照内部总和守恒。
    assert.equal(totalTorn, 0, '检测到撕裂读！');
    assert.equal(store.stats().torn, 0);

    // 终态快照总和守恒。
    const finalSnap = store.snapshot();
    assert.equal(finalSnap, COMMITS);
    assert.equal(store.sum(finalSnap), EXPECTED_SUM);
  },
);
