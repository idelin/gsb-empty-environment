import { test } from "node:test";
import assert from "node:assert/strict";
import { count, expand, type ExpandInput } from "../src/index.ts";

const base: ExpandInput = {
  template: { p: { $param: "p" }, q: { $param: "q" } },
  axes: [
    { name: "p", domain: [1, 2] },
    { name: "q", domain: [1, 2, 3] },
  ],
};

test("implies constraint (finite-domain dependency) filters combinations", () => {
  const input: ExpandInput = {
    ...base,
    constraints: [
      {
        kind: "implies",
        if: { param: "p", equals: 2 },
        then: { param: "q", in: [2, 3] },
      },
    ],
  };
  assert.deepEqual(expand(input).records, [
    { p: 1, q: 1 },
    { p: 1, q: 2 },
    { p: 1, q: 3 },
    { p: 2, q: 2 },
    { p: 2, q: 3 },
  ]);
  assert.equal(count(input), 5);
});

test("mutex constraint excludes simultaneous matches", () => {
  const input: ExpandInput = {
    ...base,
    constraints: [
      {
        kind: "mutex",
        conditions: [
          { param: "p", equals: 1 },
          { param: "q", equals: 1 },
        ],
      },
    ],
  };
  assert.deepEqual(expand(input).records, [
    { p: 1, q: 2 },
    { p: 1, q: 3 },
    { p: 2, q: 1 },
    { p: 2, q: 2 },
    { p: 2, q: 3 },
  ]);
});

test("constraint propagation skips infeasible subtrees entirely", () => {
  // p=1 forbids every q value: the whole p=1 branch must disappear.
  const input: ExpandInput = {
    ...base,
    constraints: [
      {
        kind: "implies",
        if: { param: "p", equals: 1 },
        then: { param: "q", in: [] },
      },
    ],
  };
  assert.deepEqual(expand(input).records, [
    { p: 2, q: 1 },
    { p: 2, q: 2 },
    { p: 2, q: 3 },
  ]);
});

test("contradictory constraints yield zero records", () => {
  const input: ExpandInput = {
    ...base,
    constraints: [
      {
        kind: "implies",
        if: { param: "p", in: [1, 2] },
        then: { param: "q", in: [] },
      },
    ],
  };
  const result = expand(input);
  assert.equal(result.records.length, 0);
  assert.equal(result.total, 0);
});

test("compound conditions: not/all/any", () => {
  const input: ExpandInput = {
    ...base,
    constraints: [
      {
        kind: "mutex",
        conditions: [
          {
            any: [
              { param: "p", equals: 1 },
              { param: "p", equals: 2 },
            ],
          },
          { not: { param: "q", equals: 3 } },
          { all: [{ param: "q", in: [1, 2] }] },
        ],
      },
    ],
  };
  // Excludes q in {1,2} for every p; only q=3 survives.
  assert.deepEqual(expand(input).records, [
    { p: 1, q: 3 },
    { p: 2, q: 3 },
  ]);
});
