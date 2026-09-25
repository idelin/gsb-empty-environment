import { test } from "node:test";
import assert from "node:assert/strict";
import { count, estimate, expand, type ExpandInput } from "../src/index.ts";

const twoByThree: ExpandInput = {
  template: { pair: [{ $param: "a" }, { $param: "b" }] },
  axes: [
    { name: "a", domain: [1, 2] },
    { name: "b", domain: ["x", "y", "z"] },
  ],
};

test("2x3 axes produce 6 records in deterministic order (last axis fastest)", () => {
  const result = expand(twoByThree);
  assert.equal(result.records.length, 6);
  assert.equal(result.complete, true);
  assert.equal(result.total, 6);
  assert.deepEqual(result.records, [
    { pair: [1, "x"] },
    { pair: [1, "y"] },
    { pair: [1, "z"] },
    { pair: [2, "x"] },
    { pair: [2, "y"] },
    { pair: [2, "z"] },
  ]);
});

test("repeated runs produce identical order", () => {
  const first = expand(twoByThree).records;
  const second = expand(twoByThree).records;
  assert.deepEqual(first, second);
});

test("empty axis produces 0 records", () => {
  const input: ExpandInput = {
    template: { v: { $param: "a" } },
    axes: [
      { name: "a", domain: [] },
      { name: "b", domain: [1, 2, 3] },
    ],
  };
  const result = expand(input);
  assert.equal(result.records.length, 0);
  assert.equal(result.complete, true);
  assert.equal(result.total, 0);
  assert.equal(result.cursor, null);
  assert.equal(count(input), 0);
  assert.equal(estimate(input), 0);
});

test("estimate is the raw domain product, count respects constraints", () => {
  const input: ExpandInput = {
    ...twoByThree,
    constraints: [
      {
        kind: "implies",
        if: { param: "a", equals: 2 },
        then: { param: "b", in: ["x", "y"] },
      },
    ],
  };
  assert.equal(estimate(input), 6);
  assert.equal(count(input), 5);
});

test("zero axes yield exactly one record (empty tuple)", () => {
  const result = expand({ template: { fixed: true }, axes: [] });
  assert.deepEqual(result.records, [{ fixed: true }]);
  assert.equal(result.total, 1);
});
