import { test } from "node:test";
import assert from "node:assert/strict";
import { FakeClock } from "../src/clock.ts";
import { SlidingWindowCounter } from "../src/slidingWindow.ts";

test("滑动窗口：统计窗口内的累计值", () => {
  const clock = new FakeClock();
  const counter = new SlidingWindowCounter(1000, 10, clock);
  counter.add(100);
  clock.advance(300);
  counter.add(50);
  assert.equal(counter.sum(), 150);
});

test("滑动窗口：超过窗口后旧数据衰减为 0", () => {
  const clock = new FakeClock();
  const counter = new SlidingWindowCounter(1000, 10, clock);
  counter.add(300);
  assert.equal(counter.sum(), 300);
  clock.advance(1100);
  assert.equal(counter.sum(), 0);
});

test("滑动窗口：窗口滑动时逐桶过期", () => {
  const clock = new FakeClock();
  const counter = new SlidingWindowCounter(1000, 10, clock);
  counter.add(100); // 落在第一个桶
  clock.advance(950); // 第一个桶即将滑出
  assert.equal(counter.sum(), 100);
  clock.advance(200); // 第一个桶已滑出窗口
  assert.equal(counter.sum(), 0);
});
