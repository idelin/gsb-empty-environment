import { test } from 'node:test';
import assert from 'node:assert/strict';
import { AtomicMPMCQueue, LockFreeQueue, MutexQueue } from '../src/queue.ts';

test('capacity must be a positive power of two', () => {
  for (const bad of [0, -1, 1, 3, 5, 6, 7, 100, 1.5, NaN]) {
    assert.throws(() => new AtomicMPMCQueue(bad), RangeError);
  }
  for (const good of [2, 4, 8, 1024]) {
    assert.ok(new AtomicMPMCQueue(good));
  }
});

test('dequeue on empty queue returns undefined', () => {
  const q = new AtomicMPMCQueue(4);
  assert.equal(q.dequeue(), undefined);
  assert.equal(q.dequeue(), undefined);
  assert.equal(q.size, 0);
  assert.ok(q.isEmpty);
});

test('enqueue on full queue returns false without corruption', () => {
  const q = new AtomicMPMCQueue(4);
  for (let i = 0; i < 4; i++) assert.ok(q.enqueue(i + 1));
  assert.ok(q.isFull);
  assert.equal(q.enqueue(99), false);
  assert.equal(q.enqueue(100), false);
  assert.equal(q.size, 4);
  assert.deepEqual(
    [q.dequeue(), q.dequeue(), q.dequeue(), q.dequeue()],
    [1, 2, 3, 4],
  );
  assert.equal(q.dequeue(), undefined);
});

test('capacity-2 queue alternates correctly at the boundary', () => {
  const q = new AtomicMPMCQueue(2);
  assert.ok(q.enqueue(7));
  assert.ok(q.enqueue(8));
  assert.equal(q.enqueue(9), false);
  assert.equal(q.dequeue(), 7);
  assert.ok(q.enqueue(9));
  assert.equal(q.enqueue(10), false);
  assert.equal(q.dequeue(), 8);
  assert.equal(q.dequeue(), 9);
  assert.equal(q.dequeue(), undefined);
});

test('FIFO order preserved across heavy wraparound', () => {
  const q = new AtomicMPMCQueue(8);
  let nextIn = 0;
  let nextOut = 0;
  for (let round = 0; round < 100_000; round++) {
    if (nextIn - nextOut < 8 && q.enqueue(nextIn)) nextIn++;
    const v = q.dequeue();
    if (v !== undefined) {
      assert.equal(v, nextOut);
      nextOut++;
    }
  }
  assert.equal(nextOut, nextIn);
  assert.ok(q.isEmpty);
});

test('fill-drain cycles keep sequence numbers consistent', () => {
  const q = new AtomicMPMCQueue(4);
  for (let cycle = 0; cycle < 1000; cycle++) {
    for (let i = 0; i < 4; i++) assert.ok(q.enqueue(cycle * 4 + i));
    for (let i = 0; i < 4; i++) assert.equal(q.dequeue(), cycle * 4 + i);
  }
  assert.ok(q.isEmpty);
  assert.equal(q.enqueue(-1), true);
  assert.equal(q.dequeue(), -1);
});

test('negative and boundary int32 values roundtrip', () => {
  const q = new AtomicMPMCQueue(8);
  const values = [0, -1, 1, 2147483647, -2147483648, 123456789, -987654321];
  for (const v of values) assert.ok(q.enqueue(v));
  for (const v of values) assert.equal(q.dequeue(), v);
});

test('shared buffer view sees identical state', () => {
  const q = new AtomicMPMCQueue(4);
  q.enqueue(11);
  q.enqueue(22);
  const peer = AtomicMPMCQueue.fromSharedArrayBuffer(q.buffer, 4);
  assert.equal(peer.size, 2);
  assert.equal(peer.dequeue(), 11);
  assert.equal(q.dequeue(), 22);
  assert.ok(peer.isEmpty && q.isEmpty);
});

test('LockFreeQueue generic: boundaries and FIFO', () => {
  const q = new LockFreeQueue<string>(4);
  assert.equal(q.dequeue(), undefined);
  assert.ok(q.enqueue('a'));
  assert.ok(q.enqueue('b'));
  assert.ok(q.enqueue('c'));
  assert.ok(q.enqueue('d'));
  assert.equal(q.enqueue('e'), false);
  assert.ok(q.isFull);
  assert.deepEqual([q.dequeue(), q.dequeue()], ['a', 'b']);
  assert.ok(q.enqueue('e'));
  assert.deepEqual([q.dequeue(), q.dequeue(), q.dequeue()], ['c', 'd', 'e']);
  assert.equal(q.dequeue(), undefined);
  assert.ok(q.isEmpty);
});

test('LockFreeQueue: no reference leaks after drain', () => {
  const q = new LockFreeQueue<object>(8);
  for (let round = 0; round < 50; round++) {
    for (let i = 0; i < 8; i++) q.enqueue({ id: round * 8 + i });
    for (let i = 0; i < 8; i++) q.dequeue();
    assert.equal(q.countLiveSlots(), 0, `leak after round ${round}`);
  }
  for (let i = 0; i < 5; i++) q.enqueue({ id: i });
  assert.equal(q.countLiveSlots(), 5);
  for (let i = 0; i < 5; i++) q.dequeue();
  assert.equal(q.countLiveSlots(), 0);
});

test('LockFreeQueue: slots recycle without duplication', () => {
  const q = new LockFreeQueue<number>(4);
  const seen: number[] = [];
  for (let i = 0; i < 10_000; i++) {
    assert.ok(q.enqueue(i));
    const v = q.dequeue();
    assert.equal(v, i);
    seen.push(v as number);
  }
  assert.equal(new Set(seen).size, seen.length);
});

test('MutexQueue baseline behaves as FIFO', () => {
  const q = new MutexQueue(3);
  assert.equal(q.dequeue(), undefined);
  assert.ok(q.enqueue(1));
  assert.ok(q.enqueue(2));
  assert.ok(q.enqueue(3));
  assert.equal(q.enqueue(4), false);
  assert.deepEqual([q.dequeue(), q.dequeue(), q.dequeue()], [1, 2, 3]);
  assert.equal(q.dequeue(), undefined);
});
