import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Worker } from 'node:worker_threads';
import { CASBasedTokenBucket, TOKEN_SCALE } from '../src/token-bucket.ts';

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

test('基本获取：扣减令牌，耗尽后拒绝', () => {
  const bucket = new CASBasedTokenBucket({ capacity: 3, refillRate: 0 });
  assert.equal(bucket.tryAcquire(1), true);
  assert.equal(bucket.tryAcquire(2), true);
  assert.equal(bucket.tryAcquire(1), false); // 已耗尽
  assert.equal(bucket.availableTokens(), 0);
});

test('获取失败不扣减任何令牌', () => {
  const bucket = new CASBasedTokenBucket({ capacity: 3, refillRate: 0 });
  assert.equal(bucket.tryAcquire(5), false); // 超过容量，直接拒绝
  assert.equal(bucket.tryAcquire(4), false); // 超过剩余，拒绝且不扣减
  assert.equal(bucket.availableTokens(), 3);
  assert.equal(bucket.tryAcquire(3), true);
  assert.equal(bucket.availableTokens(), 0);
});

test('随时间补充令牌，且不超过容量上限', async () => {
  const bucket = new CASBasedTokenBucket({ capacity: 200, refillRate: 1000 }); // 1 token/ms
  assert.equal(bucket.tryAcquire(200), true); // 清空
  await sleep(120);
  const avail = bucket.availableTokens();
  assert.ok(avail >= 100, `应至少补充 100，实际 ${avail}`);
  assert.ok(avail <= 200, `不应超过容量 200，实际 ${avail}`);

  const capped = new CASBasedTokenBucket({ capacity: 50, refillRate: 1000 });
  await sleep(100); // 满桶状态下时间流逝，令牌不溢出
  assert.equal(capped.availableTokens(), 50);
});

test('速率整形：单位时间放行量不超过 初始 + 应补充', async () => {
  const bucket = new CASBasedTokenBucket({
    capacity: 1_000_000,
    refillRate: 1000,
    initialTokens: 0,
  });
  const deadline = Date.now() + 150;
  let acquired = 0;
  while (Date.now() < deadline) {
    if (bucket.tryAcquire(1)) acquired += 1;
  }
  const snap = bucket.inspect();
  // 精确守恒：已放行 + 剩余 === 已铸造（桶从未触顶，无丢弃）
  assert.equal(acquired * TOKEN_SCALE + snap.tokensFp, snap.mintedFp);
  assert.ok(snap.tokensFp >= 0, '剩余令牌不为负（无透支）');
  console.log(
    `    单线程 150ms：放行 ${acquired}，理论铸造 ${snap.mintedFp / TOKEN_SCALE} token`,
  );
});

test('异步 acquire：等待补充后成功，超时返回 false', async () => {
  const bucket = new CASBasedTokenBucket({ capacity: 1, refillRate: 100 }); // 10ms/token
  assert.equal(await bucket.acquire(1), true); // 立即成功
  assert.equal(await bucket.acquire(1, 100), true); // 等待补充后成功
  assert.equal(await bucket.acquire(5, 20), false); // 超容量请求，超时失败
});

test('验收：100 个并发 Worker 高频 Acquire，无锁、无死锁、总量精确守恒', { timeout: 60_000 }, async () => {
  const WORKERS = 100;
  const capacity = 500_000;
  const refillRate = 100_000; // 10 万 token/秒
  const durationMs = 400;

  const bucket = new CASBasedTokenBucket({ capacity, refillRate, initialTokens: 0 });

  const started = Date.now();
  const results = await Promise.all(
    Array.from({ length: WORKERS }, () =>
      new Promise<number>((resolve, reject) => {
        const worker = new Worker(new URL('../src/worker.ts', import.meta.url), {
          workerData: { buffer: bucket.buffer, capacity, refillRate, durationMs },
        });
        worker.once('message', (msg: { acquired: number }) => resolve(msg.acquired));
        worker.once('error', reject);
        worker.once('exit', (code) => {
          if (code !== 0) reject(new Error(`worker exited with code ${code}`));
        });
      }),
    ),
  );
  const wallMs = Date.now() - started;

  const totalAcquired = results.reduce((sum, n) => sum + n, 0);
  const snap = bucket.inspect();

  // 核心验收：已放行 + 剩余 === 理论铸造（定点精度，完全吻合）
  assert.equal(
    totalAcquired * TOKEN_SCALE + snap.tokensFp,
    snap.mintedFp,
    '守恒恒等式不成立：出现脏数据或透支',
  );
  assert.ok(snap.tokensFp >= 0, '剩余令牌不为负（无透支）');
  assert.ok(totalAcquired > 0, '应有令牌被放行');
  // 速率上界：放行量绝不超过理论铸造量
  assert.ok(totalAcquired * TOKEN_SCALE <= snap.mintedFp);

  console.log(
    `    ${WORKERS} workers × ${durationMs}ms（墙钟 ${wallMs}ms）：` +
      `总放行 ${totalAcquired} token，理论铸造 ${snap.mintedFp / TOKEN_SCALE}，` +
      `剩余 ${snap.tokens}，守恒校验精确通过`,
  );
});
