import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Worker } from 'node:worker_threads';
import { CasTokenBucket, SCALE, scaledElapsedTokens } from '../src/token-bucket.ts';

/** 可手动推进的虚拟时钟, 用于确定性测试 */
function manualClock(start = 1_000_000) {
  let now = start;
  return {
    now: () => now,
    advance(ms: number) {
      now += ms;
    },
  };
}

test('确定性补给: 按虚拟时钟精确累积与扣减', () => {
  const clock = manualClock();
  const bucket = new CasTokenBucket({
    capacity: 100,
    refillRatePerSec: 1000, // 1 令牌 / ms
    initialTokens: 0,
    now: clock.now,
  });

  assert.equal(bucket.availableTokens(), 0);
  assert.equal(bucket.tryAcquire(1), false);

  clock.advance(10); // +10 令牌
  assert.equal(bucket.availableTokens(), 10);
  assert.equal(bucket.tryAcquire(10), true);
  assert.equal(bucket.tryAcquire(1), false);

  clock.advance(1); // +1 令牌
  assert.equal(bucket.tryAcquire(1), true);
  assert.equal(bucket.availableTokens(), 0);
});

test('容量钳制: 补给不会超过 capacity', () => {
  const clock = manualClock();
  const bucket = new CasTokenBucket({
    capacity: 50,
    refillRatePerSec: 1000,
    initialTokens: 40,
    now: clock.now,
  });
  clock.advance(10_000); // 理论 +10000, 应被钳制到 50
  assert.equal(bucket.availableTokens(), 50);
  assert.equal(bucket.tryAcquire(50), true);
  assert.equal(bucket.tryAcquire(1), false);
});

test('定点精度: 亚毫秒级速率无累计舍入误差', () => {
  const clock = manualClock();
  const bucket = new CasTokenBucket({
    capacity: 4_000_000,
    refillRatePerSec: 100, // 0.1 令牌 / ms
    initialTokens: 0,
    now: clock.now,
  });
  // 分 10 次各推进 1ms, 等价于一次性推进 10ms (绝对时间函数, 无累计误差)
  for (let i = 0; i < 10; i++) clock.advance(1);
  assert.equal(bucket.availableTokens(), 1);
});

test('不变量: 已放行 + 剩余 == 初始 + 累计补给 (单线程)', () => {
  const clock = manualClock();
  const rate = 64_000;
  const initial = 1_000;
  const bucket = new CasTokenBucket({
    capacity: 4_000_000,
    refillRatePerSec: rate,
    initialTokens: initial,
    now: clock.now,
  });

  let acquired = 0n;
  for (let i = 0; i < 500; i++) {
    clock.advance(1);
    if (bucket.tryAcquire(7)) acquired += 7n;
  }
  const snap = bucket.snapshot();
  const rateScaledPerSec = BigInt(Math.round(rate * Number(SCALE)));
  const theoretical = BigInt(initial) * SCALE + scaledElapsedTokens(snap.lastRefillMs, rateScaledPerSec);
  assert.equal(acquired * SCALE + snap.availableScaled, theoretical);
});

test('验收: 100 个并发 Worker 高频争抢, 无锁无死锁, 总量与理论值完全吻合', { timeout: 60_000 }, async () => {
  const WORKERS = 100;
  const DURATION_MS = 2_000;
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

  let reported = 0n;
  const workers: Worker[] = [];
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
  await new Promise((resolve) => setTimeout(resolve, DURATION_MS));
  Atomics.store(stopFlag, 0, 1);

  // 全部 Worker 正常退出 (无死锁的直接证据: 测试能在超时前完成)
  await Promise.all(
    workers.map(
      (worker) =>
        new Promise<void>((resolve, reject) => {
          worker.once('exit', (code) =>
            code === 0 ? resolve() : reject(new Error(`worker 异常退出, code=${code}`)),
          );
          worker.once('error', reject);
        }),
    ),
  );

  const snap = bucket.snapshot();
  const totalAcquired = Atomics.load(acquiredCounter, 0);

  // 无脏数据: 两条独立的计数路径必须一致
  assert.equal(reported, totalAcquired, 'Worker 上报合计与共享原子计数器不一致');
  assert.ok(totalAcquired > 0n, '没有放行任何令牌, 不符合预期');

  // 核心不变量: 放行 + 剩余 == 初始 + 累计补给 (定点整数, 精确相等)
  const rateScaledPerSec = BigInt(Math.round(RATE_PER_SEC * Number(SCALE)));
  const theoretical =
    BigInt(INITIAL_TOKENS) * SCALE + scaledElapsedTokens(snap.lastRefillMs, rateScaledPerSec);
  const actual = totalAcquired * SCALE + snap.availableScaled;
  assert.equal(actual, theoretical, '总放行数量与理论值不吻合');

  // 令牌数永远在合法区间内
  assert.ok(snap.availableScaled >= 0n);
  assert.ok(snap.availableScaled <= BigInt(CAPACITY) * SCALE);

  console.log(
    `    [并发验收] workers=${WORKERS} 放行=${totalAcquired} 剩余=${Number(snap.availableScaled) / Number(SCALE)} ` +
      `理论=${theoretical} 实际=${actual} 完全吻合`,
  );
});
