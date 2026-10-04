import { test } from "node:test";
import assert from "node:assert/strict";
import { MinHeap } from "../src/heap.ts";

test("min-heap pops elements in sorted order", () => {
  const h = new MinHeap<number>((a, b) => a - b);
  for (const x of [5, 1, 9, 3, 7, 3, 0]) h.push(x);
  const out: number[] = [];
  while (h.size > 0) out.push(h.pop()!);
  assert.deepEqual(out, [0, 1, 3, 3, 5, 7, 9]);
});

test("empty heap pops undefined", () => {
  const h = new MinHeap<number>((a, b) => a - b);
  assert.equal(h.pop(), undefined);
  assert.equal(h.peek(), undefined);
});

test("interleaved push and pop keeps order", () => {
  const h = new MinHeap<number>((a, b) => a - b);
  h.push(4);
  h.push(2);
  assert.equal(h.pop(), 2);
  h.push(3);
  h.push(1);
  assert.equal(h.pop(), 1);
  assert.equal(h.pop(), 3);
  assert.equal(h.pop(), 4);
});
