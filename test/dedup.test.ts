import { test } from "node:test";
import assert from "node:assert/strict";
import { count, expand, type ExpandInput } from "../src/index.ts";

const dedupInput: ExpandInput = {
  template: { id: { $param: "k" }, val: { $param: "v" } },
  axes: [
    { name: "k", domain: [1, 1, 2] },
    { name: "v", domain: ["a", "b"] },
  ],
  identity: ["$.id"],
};

test("duplicate values are deduped by declared identity, first occurrence wins", () => {
  const result = expand(dedupInput);
  assert.deepEqual(result.records, [
    { id: 1, val: "a" },
    { id: 2, val: "a" },
  ]);
  assert.equal(result.total, 2);
  assert.equal(count(dedupInput), 2);
});

test("without declared identity duplicates are kept", () => {
  const { identity, ...rest } = dedupInput;
  const result = expand(rest);
  assert.equal(result.records.length, 6);
});

test("dedup state survives budget interruption and resume", () => {
  const first = expand(dedupInput, { budget: 1 });
  assert.deepEqual(first.records, [{ id: 1, val: "a" }]);
  assert.equal(first.complete, false);
  assert.ok(first.cursor);

  const second = expand(dedupInput, { cursor: first.cursor! });
  // The duplicate {id:1} rows must not reappear after resuming.
  assert.deepEqual(second.records, [{ id: 2, val: "a" }]);
  assert.equal(second.complete, true);
  assert.equal(second.total, 2);
});

test("large product with small budget does not expand the full product", () => {
  const big: ExpandInput = {
    template: { x: { $param: "x" }, y: { $param: "y" } },
    axes: [
      { name: "x", domain: Array.from({ length: 5000 }, (_, i) => i) },
      { name: "y", domain: Array.from({ length: 5000 }, (_, i) => i) },
    ],
  };
  const start = performance.now();
  const result = expand(big, { budget: 3 });
  const elapsed = performance.now() - start;
  assert.equal(result.records.length, 3);
  assert.equal(result.complete, false);
  assert.equal(result.total, null); // never a guessed total
  assert.ok(result.cursor);
  assert.ok(elapsed < 1000, `took ${elapsed}ms; full product would be 25M rows`);
});
