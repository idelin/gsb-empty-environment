import { test } from "node:test";
import assert from "node:assert/strict";
import { at, atReverse, expand, type ExpandInput } from "../src/index.ts";

const input: ExpandInput = {
  template: { pair: [{ $param: "a" }, { $param: "b" }] },
  axes: [
    { name: "a", domain: [1, 2] },
    { name: "b", domain: ["x", "y", "z"] },
  ],
};

test("at(rank) locates records by 0-based rank", () => {
  assert.deepEqual(at(input, 0), { pair: [1, "x"] });
  assert.deepEqual(at(input, 3), { pair: [2, "x"] });
  assert.deepEqual(at(input, 5), { pair: [2, "z"] });
  assert.equal(at(input, 6), null);
  assert.equal(at(input, 100), null);
});

test("negative ranks count from the end", () => {
  assert.deepEqual(at(input, -1), { pair: [2, "z"] });
  assert.deepEqual(at(input, -6), { pair: [1, "x"] });
  assert.equal(at(input, -7), null);
});

test("atReverse(k) ranks from the end, k=0 is last", () => {
  assert.deepEqual(atReverse(input, 0), { pair: [2, "z"] });
  assert.deepEqual(atReverse(input, 5), { pair: [1, "x"] });
  assert.equal(atReverse(input, 6), null);
});

test("rank positioning respects constraints and dedup", () => {
  const constrained: ExpandInput = {
    ...input,
    constraints: [
      {
        kind: "implies",
        if: { param: "a", equals: 2 },
        then: { param: "b", in: ["y", "z"] },
      },
    ],
  };
  const all = expand(constrained).records;
  for (let i = 0; i < all.length; i++) {
    assert.deepEqual(at(constrained, i), all[i]);
    assert.deepEqual(atReverse(constrained, all.length - 1 - i), all[i]);
  }
});

test("at() on an empty axis returns null", () => {
  const empty: ExpandInput = {
    template: { v: { $param: "a" } },
    axes: [{ name: "a", domain: [] }],
  };
  assert.equal(at(empty, 0), null);
  assert.equal(atReverse(empty, 0), null);
});
