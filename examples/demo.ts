/**
 * 运行样例: 100 个并发 Worker 高频争抢一个无锁令牌桶, 结束后做对账。
 */
import { Worker } from 'node:worker_threads';
import { CasTokenBucket, SCALE, scaledElapsedTokens } from '../src/token-bucket.ts';

const WORKERS = 100;
const DURATION_MS = 1_000;
const CAPACITY = 4_000_000;
const RATE_PER_SEC = 64_000;
const INITIAL_TOKENS = 1_000;

const bucket = new CasTokenBucket({
  capacity: CAPACITY,
  refillRatePerSec: RATE_PER_SEC,
  initialTokens: INITIAL_TOKENS,
});

const controlBuffer = new SharedArrayBuffer(16);
const stopFlag = new Int32Array(controlBuffer, 0, 1);
const acquiredCounter = new BigUint64Array(controlBuffer, 8, 1);

console.log(`启动 ${WORKERS} 个 Worker, 高频调用 tryAcquire(), 持续 ${DURATION_MS}ms ...`);
console.log(`桶参数: capacity=${CAPACITY}, rate=${RATE_PER_SEC}/s, initial=${INITIAL_TOKENS}`);

const workers: Worker[] = [];
let reported = 0n;
for (let i = 0; i < WORKERS; i++) {
  const worker = new Worker(new URL('../src/worker.ts', import.meta.url), {
    workerData: {
      buffer: bucket.buffer,
      controlBuffer,
      capacity: CAPACITY,
      refillRatePerSec: RATE_PER_SEC,
    },
  });
  worker.on('message', (msg: { acquired: number }) => {
    reported += BigInt(msg.acquired);
  });
  workers.push(worker);
}

// 等全部 Worker 就绪后再开始计时, 避免线程启动开销挤占压测窗口
await Promise.all(workers.map((worker) => new Promise<void>((resolve) => worker.once('online', resolve))));
console.log('全部 Worker 就绪, 开始压测 ...');
await new Promise((resolve) => setTimeout(resolve, DURATION_MS));
Atomics.store(stopFlag, 0, 1);
await Promise.all(
  workers.map(
    (worker) =>
      new Promise<void>((resolve, reject) => {
        worker.once('exit', (code) => (code === 0 ? resolve() : reject(new Error(`worker exit ${code}`))));
        worker.once('error', reject);
      }),
  ),
);

// 所有 Worker 已退出, 结算到当前时刻并对账
const snap = bucket.snapshot();
const totalAcquired = Atomics.load(acquiredCounter, 0);
const rateScaledPerSec = BigInt(Math.round(RATE_PER_SEC * Number(SCALE)));
const theoretical =
  BigInt(INITIAL_TOKENS) * SCALE + scaledElapsedTokens(snap.lastRefillMs, rateScaledPerSec);
const actual = totalAcquired * SCALE + snap.availableScaled;

console.log('');
console.log(`总放行令牌:        ${totalAcquired}`);
console.log(`Worker 上报合计:   ${reported} (与共享计数器一致: ${reported === totalAcquired})`);
console.log(`桶内剩余(scaled):  ${snap.availableScaled}`);
console.log(`理论总量(scaled):  ${theoretical}`);
console.log(`实际总量(scaled):  ${actual}`);
console.log(`对账结果:          ${actual === theoretical ? '完全吻合 ✓ (无死锁、无脏数据、无互斥锁)' : '不匹配 ✗'}`);
