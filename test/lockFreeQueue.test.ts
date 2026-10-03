import test from 'node:test';
import assert from 'node:assert/strict';
import { LockFreeQueue } from '../src/lockfree/lockFreeQueue.ts';

test('空队列出队返回 undefined，初始即静默状态', () => {
  const q = new LockFreeQueue<number>();
  assert.equal(q.dequeue(), undefined);
  assert.equal(q.size, 0);
  assert.ok(q.isEmpty);
  assert.ok(q.isQuiescent());
});

test('FIFO：单线程入队出队保持顺序', () => {
  const q = new LockFreeQueue<number>();
  for (let i = 0; i < 100; i++) q.enqueue(i);
  assert.equal(q.size, 100);
  for (let i = 0; i < 100; i++) assert.equal(q.dequeue(), i);
  assert.equal(q.dequeue(), undefined);
  assert.ok(q.isQuiescent());
});

test('交错入队出队与参考实现（数组）完全一致', () => {
  const q = new LockFreeQueue<number>();
  const ref: number[] = [];
  let seed = 42;
  const rand = (): number => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed;
  };
  for (let step = 0; step < 50_000; step++) {
    if (rand() % 2 === 0 || ref.length === 0) {
      const v = rand();
      q.enqueue(v);
      ref.push(v);
    } else {
      assert.equal(q.dequeue(), ref.shift());
    }
    assert.equal(q.size, ref.length);
  }
  while (ref.length > 0) assert.equal(q.dequeue(), ref.shift());
  assert.equal(q.size, 0);
  assert.ok(q.isQuiescent());
});

test('出队后节点引用被断开，无对象泄漏', () => {
  const q = new LockFreeQueue<object>();
  const a = {};
  const b = {};
  q.enqueue(a);
  q.enqueue(b);
  assert.equal(q.dequeue(), a);
  assert.equal(q.dequeue(), b);
  assert.equal(q.dequeue(), undefined);
  // 队列静默：头尾指针重合、尾节点无后继、无残留值引用
  assert.ok(q.isQuiescent());
  assert.equal(q.size, 0);
});

test('百万级压力：全部出队后严格无残留', () => {
  const q = new LockFreeQueue<number>();
  const N = 1_000_000;
  for (let i = 0; i < N; i++) q.enqueue(i);
  assert.equal(q.size, N);
  let sum = 0;
  for (let i = 0; i < N; i++) sum += q.dequeue() as number;
  assert.equal(sum, (N * (N - 1)) / 2);
  assert.equal(q.size, 0);
  assert.equal(q.dequeue(), undefined);
  assert.ok(q.isQuiescent());
});

test('同一元素可重复入队，互不影响', () => {
  const q = new LockFreeQueue<string>();
  q.enqueue('x');
  q.enqueue('x');
  q.enqueue('x');
  assert.equal(q.size, 3);
  assert.deepEqual([q.dequeue(), q.dequeue(), q.dequeue()], ['x', 'x', 'x']);
  assert.ok(q.isQuiescent());
});
