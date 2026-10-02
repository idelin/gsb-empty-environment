/**
 * 运行样例：1 写线程 + 5 读线程，实时打印吞吐与撕裂计数。
 * 用法：npm run demo  （或 STRESS_COMMITS=1000000 npm run demo）
 */
import { Worker } from 'node:worker_threads';
import { MvccStore } from './store.ts';

const KEY_COUNT = 1000;
const INITIAL_VALUE = 1000;
const EXPECTED_SUM = KEY_COUNT * INITIAL_VALUE;
const READER_COUNT = 5;
const COMMITS = Number(process.env.STRESS_COMMITS ?? 1_000_000);

const poolCapacity = KEY_COUNT + COMMITS * 2 + 16;
const store = MvccStore.create(KEY_COUNT, poolCapacity);
store.initAll(INITIAL_VALUE);

console.log(`启动压测：1 写线程 × ${READER_COUNT} 读线程 × ${KEY_COUNT} 键，目标写事务 ${COMMITS.toLocaleString()} 笔`);

const runWorker = (script: string, workerData: unknown): Promise<unknown> =>
  new Promise((resolve, reject) => {
    const worker = new Worker(new URL(script, import.meta.url), { workerData });
    worker.once('message', resolve);
    worker.once('error', reject);
  });

const startedAt = performance.now();
const timer = setInterval(() => {
  const s = store.stats();
  const secs = (performance.now() - startedAt) / 1000;
  console.log(
    `  [${secs.toFixed(1)}s] 写事务=${s.commits.toLocaleString()} (${Math.round(s.commits / secs).toLocaleString()} tx/s) ` +
      `读扫描=${s.scans.toLocaleString()} (${Math.round(s.scans / secs).toLocaleString()} scan/s) 撕裂=${s.torn}`,
  );
}, 1000);

const writer = runWorker('./workers/writer.worker.ts', {
  sab: store.sab,
  keyCount: KEY_COUNT,
  poolCapacity,
  commits: COMMITS,
  initialValue: INITIAL_VALUE,
  seed: 42,
});
const readers = Array.from({ length: READER_COUNT }, () =>
  runWorker('./workers/reader.worker.ts', {
    sab: store.sab,
    keyCount: KEY_COUNT,
    poolCapacity,
    expectedSum: EXPECTED_SUM,
  }),
);

await Promise.all([writer, ...readers]);
clearInterval(timer);

const secs = (performance.now() - startedAt) / 1000;
const finalStats = store.stats();
const finalSum = store.sum(store.snapshot());

console.log('─'.repeat(72));
console.log(`完成：${finalStats.commits.toLocaleString()} 笔写事务，${finalStats.scans.toLocaleString()} 次全表扫描，耗时 ${secs.toFixed(2)}s`);
console.log(`吞吐：写 ${Math.round(finalStats.commits / secs).toLocaleString()} tx/s，读 ${Math.round(finalStats.scans / secs).toLocaleString()} scan/s`);
console.log(`撕裂读：${finalStats.torn}（期望 0）`);
console.log(`终态快照总和：${finalSum.toLocaleString()}（期望 ${EXPECTED_SUM.toLocaleString()}）`);
console.log(finalStats.torn === 0 && finalSum === EXPECTED_SUM ? '✅ 校验通过：零撕裂，总和守恒' : '❌ 校验失败');
