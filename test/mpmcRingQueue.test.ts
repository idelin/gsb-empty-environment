import test from 'node:test';
import assert from 'node:assert/strict';
import { Worker } from 'node:worker_threads';
import { MpmcRingQueue } from '../src/lockfree/mpmcRingQueue.ts';

test('容量必须为 >=2 的 2 的幂', () => {
  assert.throws(() => MpmcRingQueue.create(0), RangeError);
  assert.throws(() => MpmcRingQueue.create(1), RangeError);
  assert.throws(() => MpmcRingQueue.create(3), RangeError);
  assert.throws(() => MpmcRingQueue.create(100), RangeError);
  assert.throws(() => MpmcRingQueue.create(2.5), RangeError);
  assert.ok(MpmcRingQueue.create(2));
  assert.ok(MpmcRingQueue.create(1024));
});

test('FIFO 顺序与环绕复用', () => {
  const q = MpmcRingQueue.create(4);
  for (let round = 0; round < 10; round++) {
    for (let i = 0; i < 4; i++) assert.ok(q.enqueue(round * 4 + i));
    assert.equal(q.size, 4);
    for (let i = 0; i < 4; i++) assert.equal(q.dequeue(), round * 4 + i);
    assert.equal(q.dequeue(), MpmcRingQueue.EMPTY);
  }
  assert.equal(q.size, 0);
});

test('满队列入队返回 false，空队列出队返回 EMPTY', () => {
  const q = MpmcRingQueue.create(2);
  assert.ok(q.enqueue(1));
  assert.ok(q.enqueue(2));
  assert.equal(q.enqueue(3), false); // 满
  assert.equal(q.dequeue(), 1);
  assert.ok(q.enqueue(3)); // 腾出位置后可入队
  assert.equal(q.dequeue(), 2);
  assert.equal(q.dequeue(), 3);
  assert.equal(q.dequeue(), MpmcRingQueue.EMPTY); // 空
});

test('非法元素被拒绝', () => {
  const q = MpmcRingQueue.create(2);
  assert.throws(() => q.enqueue(-1), RangeError);
  assert.throws(() => q.enqueue(1.5), RangeError);
  assert.throws(() => q.enqueue(2 ** 31), RangeError);
  assert.throws(() => q.enqueue(NaN), RangeError);
});

test('attach 依附到共享缓冲区并保留状态', () => {
  const q = MpmcRingQueue.create(8);
  q.enqueue(7);
  q.enqueue(8);
  const view = MpmcRingQueue.attach(q.buffer);
  assert.equal(view.capacity, 8);
  assert.equal(view.dequeue(), 7);
  assert.equal(view.dequeue(), 8);
  assert.equal(view.dequeue(), MpmcRingQueue.EMPTY);
});

test('attach 拒绝非法缓冲区', () => {
  assert.throws(() => MpmcRingQueue.attach(new SharedArrayBuffer(4)), RangeError);
  assert.throws(() => MpmcRingQueue.attach(new SharedArrayBuffer((2 + 3 * 2) * 4)), RangeError);
});

test('多生产者多消费者并发：元素不丢不重，队列严格排空', async () => {
  const PRODUCERS = 4;
  const CONSUMERS = 3;
  const PER_PRODUCER = 30_000;
  const TOTAL = PRODUCERS * PER_PRODUCER;

  const queue = MpmcRingQueue.create(64); // 小容量制造满/空竞争
  const statsBuffer = new SharedArrayBuffer((2 + TOTAL) * Int32Array.BYTES_PER_ELEMENT);
  const stats = new Int32Array(statsBuffer);

  const spawn = (workerData: unknown): Promise<void> =>
    new Promise<void>((resolve, reject) => {
      const worker = new Worker(new URL('../workers/ringWorker.ts', import.meta.url), { workerData });
      worker.once('error', reject);
      worker.once('exit', (code) =>
        code === 0 ? resolve() : reject(new Error(`worker exited with code ${code}`)),
      );
    });

  const jobs: Promise<void>[] = [];
  for (let p = 0; p < PRODUCERS; p++) {
    jobs.push(
      spawn({
        role: 'producer',
        buffer: queue.buffer,
        statsBuffer,
        start: p * PER_PRODUCER,
        count: PER_PRODUCER,
        numProducers: PRODUCERS,
      }),
    );
  }
  for (let c = 0; c < CONSUMERS; c++) {
    jobs.push(
      spawn({
        role: 'consumer',
        buffer: queue.buffer,
        statsBuffer,
        start: 0,
        count: 0,
        numProducers: PRODUCERS,
      }),
    );
  }
  await Promise.all(jobs);

  // 每个元素恰好被消费一次：无丢失、无重复
  assert.equal(Atomics.load(stats, 1), TOTAL);
  for (let i = 0; i < TOTAL; i++) {
    assert.equal(Atomics.load(stats, 2 + i), 1, `element ${i} consumed ${Atomics.load(stats, 2 + i)} times`);
  }
  // 严格无泄漏：队列排空、读写位置一致
  assert.equal(queue.dequeue(), MpmcRingQueue.EMPTY);
  assert.equal(queue.size, 0);
});
