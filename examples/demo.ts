import { Worker } from 'node:worker_threads';
import { CASBasedTokenBucket, TOKEN_SCALE } from '../src/token-bucket.ts';

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

// ---------- 1. 单线程基本用法 ----------
console.log('=== 单线程示例：capacity=5, refillRate=10/s ===');
const bucket = new CASBasedTokenBucket({ capacity: 5, refillRate: 10 });
for (let i = 1; i <= 7; i++) {
  console.log(`  tryAcquire(1) #${i} -> ${bucket.tryAcquire(1)}`);
}
console.log('  等待 300ms 让令牌补充...');
await sleep(300);
console.log(`  availableTokens() -> ${bucket.availableTokens().toFixed(2)}`);
console.log(`  await acquire(2) -> ${await bucket.acquire(2, 1000)}`);

// ---------- 2. 100 个并发 Worker 无锁压测 ----------
const WORKERS = 100;
const capacity = 500_000;
const refillRate = 100_000; // 10 万 token/秒
const durationMs = 500;

console.log(`\n=== 并发压测：${WORKERS} workers × ${durationMs}ms, rate=${refillRate}/s ===`);
const shared = new CASBasedTokenBucket({ capacity, refillRate, initialTokens: 0 });

const started = Date.now();
const counts = await Promise.all(
  Array.from({ length: WORKERS }, () =>
    new Promise<number>((resolve, reject) => {
      const worker = new Worker(new URL('../src/worker.ts', import.meta.url), {
        workerData: { buffer: shared.buffer, capacity, refillRate, durationMs },
      });
      worker.once('message', (msg: { acquired: number }) => resolve(msg.acquired));
      worker.once('error', reject);
    }),
  ),
);
const wallMs = Date.now() - started;

const total = counts.reduce((sum, n) => sum + n, 0);
const snap = shared.inspect();
const conserved = total * TOKEN_SCALE + snap.tokensFp === snap.mintedFp;

console.log(`  墙钟耗时:        ${wallMs} ms`);
console.log(`  总放行:          ${total} token`);
console.log(`  理论铸造:        ${snap.mintedFp / TOKEN_SCALE} token`);
console.log(`  桶内剩余:        ${snap.tokens} token`);
console.log(`  实际吞吐:        ${Math.round((total / wallMs) * 1000)} token/s`);
console.log(`  守恒校验(放行+剩余===铸造): ${conserved ? 'PASS ✔' : 'FAIL ✘'}`);
if (!conserved) process.exitCode = 1;
