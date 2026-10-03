import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Worker } from 'node:worker_threads';
import { LockFreeQueue } from '../src/lockfree-queue.ts';
import { AtomicCounter, compareAndSwap } from '../src/atomic.ts';

test('constructor rejects invalid capacities', () => {
  for (const bad of [0, 1, 3, 5, 6, 7, 100, -2, 1.5, NaN]) {
    assert.throws(() => new LockFreeQueue(bad), RangeError, `capacity=${bad}`);
  }
  for (const good of [2, 4, 8, 1024]) {
    assert.ok(new LockFreeQueue(good));
  }
});

test('attach rejects undersized buffers', () => {
  const tooSmall = new SharedArrayBuffer(LockFreeQueue.requiredBytes(8) - 4);
  assert.throws(() => LockFreeQueue.attach(tooSmall, 8), RangeError);
  const exact = new SharedArrayBuffer(LockFreeQueue.requiredBytes(8));
  new LockFreeQueue(8, exact);
  assert.ok(LockFreeQueue.attach(exact, 8)); // attach does not re-initialize
});

test('dequeue on empty queue returns null', () => {
  const q = new LockFreeQueue(4);
  assert.equal(q.dequeue(), null);
  assert.ok(q.isEmpty());
  assert.equal(q.size(), 0);
});

test('FIFO ordering for a single thread', () => {
  const q = new LockFreeQueue(8);
  for (let i = 0; i < 8; i++) assert.ok(q.enqueue(i));
  for (let i = 0; i < 8; i++) assert.equal(q.dequeue(), i);
  assert.equal(q.dequeue(), null);
});

test('full queue rejects enqueue until drained', () => {
  const q = new LockFreeQueue(4);
  assert.ok(q.enqueue(1));
  assert.ok(q.enqueue(2));
  assert.ok(q.enqueue(3));
  assert.ok(q.enqueue(4));
  assert.ok(q.isFull());
  assert.equal(q.enqueue(5), false, 'fifth enqueue must fail');
  assert.equal(q.dequeue(), 1);
  assert.ok(q.enqueue(5), 'slot frees up after dequeue');
  assert.equal(q.enqueue(6), false);
});

test('wrap-around: sequence arithmetic survives many cycles', () => {
  const q = new LockFreeQueue(4);
  const cycles = 10_000;
  for (let round = 0; round < cycles; round++) {
    for (let i = 0; i < 4; i++) assert.ok(q.enqueue(round * 4 + i));
    for (let i = 0; i < 4; i++) assert.equal(q.dequeue(), round * 4 + i);
  }
  assert.ok(q.isEmpty());
  assert.equal(q.enqueuePos, cycles * 4);
  assert.equal(q.dequeuePos, cycles * 4);
});

test('interleaved enqueue/dequeue keeps FIFO at the boundary', () => {
  const q = new LockFreeQueue(2);
  assert.ok(q.enqueue(10));
  assert.ok(q.enqueue(20));
  assert.equal(q.enqueue(30), false);
  assert.equal(q.dequeue(), 10);
  assert.ok(q.enqueue(30));
  assert.equal(q.dequeue(), 20);
  assert.equal(q.dequeue(), 30);
  assert.equal(q.dequeue(), null);
});

test('stores arbitrary int32 values including extremes', () => {
  const q = new LockFreeQueue(8);
  const values = [0, -1, 1, 2 ** 31 - 1, -(2 ** 31), 123456789, -987654321];
  for (const v of values) assert.ok(q.enqueue(v));
  for (const v of values) assert.equal(q.dequeue(), v);
});

test('compareAndSwap primitive: success and failure paths', () => {
  const view = new Int32Array(new SharedArrayBuffer(4));
  Atomics.store(view, 0, 7);
  assert.equal(compareAndSwap(view, 0, 7, 42), 7, 'observes old value');
  assert.equal(Atomics.load(view, 0), 42);
  assert.equal(compareAndSwap(view, 0, 7, 99), 42, 'failed CAS reports current');
  assert.equal(Atomics.load(view, 0), 42, 'failed CAS leaves value intact');
});

test('AtomicCounter: add/get/CAS', () => {
  const c = new AtomicCounter(10);
  assert.equal(c.get(), 10);
  assert.equal(c.add(5), 15);
  assert.equal(c.add(), 16);
  assert.ok(c.compareAndSwap(16, 100));
  assert.equal(c.get(), 100);
  assert.ok(!c.compareAndSwap(16, 200));
  assert.equal(c.get(), 100);
});

test('concurrent MPMC stress: every item delivered exactly once, zero leaks', async () => {
  const workers = 4;
  const perWorker = 25_000;
  const total = workers * perWorker;
  const capacity = 1024;

  const queue = new LockFreeQueue(capacity);
  const bitmapBuffer = new SharedArrayBuffer(total * 4);
  const counterBuffer = new SharedArrayBuffer(4);
  const bitmap = new Int32Array(bitmapBuffer);
  const counter = new Int32Array(counterBuffer);

  const handles = [];
  for (let workerId = 0; workerId < workers; workerId++) {
    handles.push(
      new Promise<{ produced: number; consumed: number }>((resolve, reject) => {
        const w = new Worker(new URL('./helpers/queue-worker.ts', import.meta.url), {
          workerData: {
            queueBuffer: queue.buffer,
            capacity,
            bitmapBuffer,
            counterBuffer,
            workerId,
            workers,
            perWorker,
          },
        });
        w.once('message', resolve);
        w.once('error', reject);
      }),
    );
  }
  const results = await Promise.all(handles);

  // Every worker produced its full share.
  assert.equal(results.reduce((s, r) => s + r.produced, 0), total);
  // Every item was consumed exactly once: no loss, no duplication.
  assert.equal(Atomics.load(counter, 0), total);
  for (let i = 0; i < total; i++) {
    assert.equal(Atomics.load(bitmap, i), 1, `item ${i} consumed != 1 times`);
  }
  // Strict no-leak: queue is empty and cursors are consistent.
  assert.ok(queue.isEmpty());
  assert.equal(queue.enqueuePos - queue.dequeuePos, 0);
  assert.equal(queue.enqueuePos, total);
});

test('shared queue across attach handles sees the same state', () => {
  const q1 = new LockFreeQueue(8);
  const q2 = LockFreeQueue.attach(q1.buffer, 8);
  assert.ok(q1.enqueue(111));
  assert.equal(q2.dequeue(), 111);
  assert.ok(q2.enqueue(222));
  assert.equal(q1.dequeue(), 222);
});
