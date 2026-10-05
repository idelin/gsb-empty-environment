import assert from "node:assert/strict";
import { test } from "node:test";
import { BoundedHistory } from "../src/history.ts";

test("有界历史：样本数量不超过容量，逐出最旧样本", () => {
  const h = new BoundedHistory(4);
  for (const v of [1, 2, 3, 4, 5, 6]) h.add(v);
  assert.equal(h.count, 4);
  assert.equal(h.mean, (3 + 4 + 5 + 6) / 4);
});

test("有界历史：均值与标准差正确", () => {
  const h = new BoundedHistory(10);
  for (const v of [2, 4, 4, 4, 5, 5, 7, 9]) h.add(v);
  assert.equal(h.mean, 5);
  assert.ok(Math.abs(h.stddev - 2) < 1e-9);
});

test("有界历史：分位数基于排序快照", () => {
  const h = new BoundedHistory(10);
  for (const v of [10, 30, 20, 50, 40]) h.add(v);
  assert.equal(h.quantile(0.5), 30);
  assert.equal(h.quantile(1), 50);
  assert.equal(h.quantile(0), 10);
});

test("有界历史：容量必须 >= 2", () => {
  assert.throws(() => new BoundedHistory(1));
});
