import { test } from "node:test";
import assert from "node:assert/strict";
import { SampleWindow } from "../src/window.ts";

test("keeps only the most recent N samples (limited history)", () => {
  const w = new SampleWindow(4);
  for (let i = 1; i <= 10; i++) w.add(i);
  assert.equal(w.size, 4);
  assert.equal(w.mean, 8.5);
});

test("computes mean and population stddev", () => {
  const w = new SampleWindow(10);
  for (const x of [2, 4, 4, 4, 5, 5, 7, 9]) w.add(x);
  assert.equal(w.mean, 5);
  assert.equal(w.stddev, 2);
});

test("empty window has zero stats", () => {
  const w = new SampleWindow(3);
  assert.equal(w.size, 0);
  assert.equal(w.mean, 0);
  assert.equal(w.stddev, 0);
});

test("rejects non-positive capacity", () => {
  assert.throws(() => new SampleWindow(0));
});
