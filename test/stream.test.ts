import { test } from "node:test";
import assert from "node:assert/strict";
import { expand, stream, type ExpandInput } from "../src/index.ts";

const input: ExpandInput = {
  template: { pair: [{ $param: "a" }, { $param: "b" }] },
  axes: [
    { name: "a", domain: [1, 2] },
    { name: "b", domain: ["x", "y", "z"] },
  ],
};

test("stream yields records lazily with sequential ranks and a summary", () => {
  const items = [];
  const it = stream(input);
  let step = it.next();
  while (!step.done) {
    items.push(step.value);
    step = it.next();
  }
  const summary = step.value;
  assert.equal(items.length, 6);
  assert.deepEqual(
    items.map((i) => i.rank),
    [0, 1, 2, 3, 4, 5],
  );
  assert.deepEqual(items[0].record, { pair: [1, "x"] });
  assert.equal(summary.complete, true);
  assert.equal(summary.total, 6);
  assert.equal(summary.cursor, null);
});

test("stream with a budget stops early and hands out a resume cursor", () => {
  const it = stream(input, { budget: 4 });
  const seen = [];
  let step = it.next();
  while (!step.done) {
    seen.push(step.value.record);
    step = it.next();
  }
  assert.equal(seen.length, 4);
  assert.equal(step.value.complete, false);
  assert.equal(step.value.total, null);
  assert.ok(step.value.cursor);

  const rest = expand(input, { cursor: step.value.cursor! });
  assert.deepEqual(
    [...seen, ...rest.records],
    expand(input).records,
  );
});

test("stream is lazy: a huge product can be sampled without full expansion", () => {
  const big: ExpandInput = {
    template: { x: { $param: "x" }, y: { $param: "y" } },
    axes: [
      { name: "x", domain: Array.from({ length: 100000 }, (_, i) => i) },
      { name: "y", domain: Array.from({ length: 100000 }, (_, i) => i) },
    ],
  };
  const it = stream(big);
  const first = it.next();
  const second = it.next();
  assert.equal(first.done, false);
  assert.deepEqual(first.value.record, { x: 0, y: 0 });
  assert.deepEqual(second.value.record, { x: 0, y: 1 });
  // Generator dropped here: no full expansion ever happened.
});

test("stream ranks stay continuous across a resumed cursor", () => {
  const first = stream(input, { budget: 5 });
  let step = first.next();
  while (!step.done) step = first.next();
  const cursor = step.value.cursor!;
  assert.ok(cursor);

  const resumed = stream(input, { cursor });
  const ranks: number[] = [];
  let r = resumed.next();
  while (!r.done) {
    ranks.push(r.value.rank);
    r = resumed.next();
  }
  assert.deepEqual(ranks, [5]);
  assert.equal(r.value.total, 6);
});
