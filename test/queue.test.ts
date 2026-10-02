import { test } from 'node:test';
import assert from 'node:assert/strict';
import { AtomicCounter, AtomicRef } from '../src/queue/atomic.ts';
import { LockFreeQueue } from '../src/queue/lockfree-queue.ts';
import { MPMCRing } from '../src/queue/mpmc-ring.ts';

// ----------------------------------------------------------- AtomicCounter --

test('AtomicCounter: load/store/add/compareExchange semantics', () => {
  const c = AtomicCounter.create(10);
  assert.equal(c.load(), 10);
  c.store(20);
  assert.equal(c.load(), 20);
  assert.equal(c.add(5), 20, 'add returns previous value');
  assert.equal(c.load(), 25);

  assert.equal(c.compareExchange(25, 100), 25, 'CAS success returns old value');
  assert.equal(c.load(), 100);
  assert.ok(c.cas(100, 200));
  assert.equal(c.load(), 200);

  assert.equal(c.compareExchange(999, 0), 200, 'CAS failure returns current value');
  assert.equal(c.load(), 200, 'failed CAS must not modify the value');
  assert.ok(!c.cas(999, 0));
  assert.equal(c.load(), 200);
});

test('AtomicCounter: shareable through its SharedArrayBuffer', () => {
  const a = AtomicCounter.create(1);
  const b = new AtomicCounter(a.buffer);
  b.add(41);
  assert.equal(a.load(), 42);
});

test('AtomicRef: CAS on object references', () => {
  const x = { id: 1 };
  const y = { id: 2 };
  const ref = new AtomicRef(x);
  assert.equal(ref.load(), x);
  assert.ok(ref.cas(x, y));
  assert.equal(ref.load(), y);
  assert.ok(!ref.cas(x, { id: 3 }));
  assert.equal(ref.load(), y);
});

// ---------------------------------------------------------- LockFreeQueue --

test('LockFreeQueue: dequeue on empty queue returns undefined', () => {
  const q = new LockFreeQueue<number>();
  assert.equal(q.dequeue(), undefined);
  assert.equal(q.size, 0);
  assert.ok(q.isEmpty);
  assert.ok(q.isCollapsedToSentinel);
});

test('LockFreeQueue: strict FIFO ordering', () => {
  const q = new LockFreeQueue<number>();
  for (let i = 0; i < 1000; i++) q.enqueue(i);
  assert.equal(q.size, 1000);
  for (let i = 0; i < 1000; i++) {
    assert.equal(q.dequeue(), i);
  }
  assert.equal(q.dequeue(), undefined);
  assert.equal(q.size, 0);
});

test('LockFreeQueue: interleaved enqueue/dequeue keeps order and count', () => {
  const q = new LockFreeQueue<string>();
  const expected: string[] = [];
  for (let round = 0; round < 500; round++) {
    q.enqueue(`a${round}`);
    q.enqueue(`b${round}`);
    expected.push(`a${round}`, `b${round}`);
    assert.equal(q.dequeue(), expected.shift());
    q.enqueue(`c${round}`);
    expected.push(`c${round}`);
    assert.equal(q.size, expected.length);
  }
  while (expected.length) {
    assert.equal(q.dequeue(), expected.shift());
  }
  assert.ok(q.isEmpty);
});

test('LockFreeQueue: stress 200k ops, drains to sentinel with zero leaks', () => {
  const q = new LockFreeQueue<number>();
  const N = 200_000;
  let nextExpected = 0;
  for (let i = 0; i < N; i++) {
    q.enqueue(i);
    if (i % 3 === 0) {
      assert.equal(q.dequeue(), nextExpected++);
    }
  }
  while (!q.isEmpty) {
    assert.equal(q.dequeue(), nextExpected++);
  }
  assert.equal(nextExpected, N);
  assert.equal(q.size, 0);
  // Leak assertion: a fully drained queue collapses to one sentinel node,
  // so no linked nodes are retained.
  assert.ok(q.isCollapsedToSentinel, 'drained queue must not retain nodes');
});

// ---------------------------------------------------------------- MPMCRing --

test('MPMCRing: capacity must be a power of two >= 2', () => {
  assert.throws(() => MPMCRing.create(0), RangeError);
  assert.throws(() => MPMCRing.create(3), RangeError);
  assert.throws(() => MPMCRing.create(1000), RangeError);
  assert.ok(MPMCRing.create(2));
  assert.ok(MPMCRing.create(1024));
});

test('MPMCRing: empty dequeue returns undefined, full enqueue returns false', () => {
  const ring = MPMCRing.create(4);
  assert.equal(ring.dequeue(), undefined);
  assert.ok(ring.isEmpty);

  assert.ok(ring.enqueue(1));
  assert.ok(ring.enqueue(2));
  assert.ok(ring.enqueue(3));
  assert.ok(ring.enqueue(4));
  assert.equal(ring.enqueue(5), false, 'ring is full');
  assert.equal(ring.size, 4);

  assert.equal(ring.dequeue(), 1);
  assert.ok(ring.enqueue(5), 'slot freed after dequeue');
  assert.equal(ring.enqueue(6), false);
});

test('MPMCRing: FIFO order preserved across wraparound cycles', () => {
  const ring = MPMCRing.create(8);
  let nextIn = 0;
  let nextOut = 0;
  // 50 full wraparound cycles of the 8-slot ring.
  for (let cycle = 0; cycle < 50; cycle++) {
    for (let i = 0; i < 6; i++) assert.ok(ring.enqueue(nextIn++));
    for (let i = 0; i < 6; i++) assert.equal(ring.dequeue(), nextOut++);
  }
  assert.ok(ring.isEmpty);
  assert.equal(nextIn, nextOut);
});

test('MPMCRing: sequence numbers stay consistent after heavy churn', () => {
  const ring = MPMCRing.create(16);
  for (let i = 0; i < 100_000; i++) {
    assert.ok(ring.enqueue(i));
    assert.equal(ring.dequeue(), i);
  }
  assert.ok(ring.isEmpty);
  assert.equal(ring.dequeue(), undefined);
});
