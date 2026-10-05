import assert from "node:assert/strict";
import { test } from "node:test";
import { EventQueue } from "../src/eventQueue.ts";

test("事件队列：按时间升序弹出", () => {
  const q = new EventQueue<string>();
  q.push(30, "c");
  q.push(10, "a");
  q.push(20, "b");
  assert.equal(q.pop()?.payload, "a");
  assert.equal(q.pop()?.payload, "b");
  assert.equal(q.pop()?.payload, "c");
  assert.equal(q.pop(), undefined);
});

test("事件队列：同一时间按入队先后弹出", () => {
  const q = new EventQueue<string>();
  q.push(10, "first");
  q.push(10, "second");
  q.push(10, "third");
  assert.equal(q.pop()?.payload, "first");
  assert.equal(q.pop()?.payload, "second");
  assert.equal(q.pop()?.payload, "third");
});

test("事件队列：交错入队出队保持有序", () => {
  const q = new EventQueue<number>();
  for (const t of [5, 1, 9, 3]) q.push(t, t);
  assert.equal(q.pop()?.payload, 1);
  q.push(2, 2);
  q.push(0, 0);
  const order: number[] = [];
  for (let e = q.pop(); e; e = q.pop()) order.push(e.payload);
  assert.deepEqual(order, [0, 2, 3, 5, 9]);
});
