import { test } from "node:test";
import assert from "node:assert/strict";
import {
  at,
  atReverse,
  count,
  expand,
  type Constraint,
  type ExpandInput,
} from "../src/index.ts";
import { verifyCompleteness } from "../src/verify.ts";

const DOMAIN_POOL: readonly (readonly unknown[])[] = [
  [],
  [0],
  [0, 1],
  [0, 0],
  ["a", "b"],
];

function constraintsFor(axisCount: number): Constraint[][] {
  const none: Constraint[] = [];
  if (axisCount === 1) {
    return [
      none,
      [
        {
          kind: "implies",
          if: { param: "a0", equals: 0 },
          then: { param: "a0", in: [1] },
        },
      ],
    ];
  }
  return [
    none,
    [
      {
        kind: "implies",
        if: { param: "a0", equals: 0 },
        then: { param: "a1", in: [0] },
      },
    ],
    [
      {
        kind: "mutex",
        conditions: [
          { param: "a0", equals: 0 },
          { param: "a1", equals: 0 },
        ],
      },
    ],
    [
      {
        kind: "implies",
        if: { param: "a0", equals: 0 },
        then: { param: "a1", in: [0] },
      },
      {
        kind: "mutex",
        conditions: [
          { param: "a0", equals: 0 },
          { param: "a1", in: [0, 1] },
        ],
      },
    ],
  ];
}

function* smallInputs(): Generator<ExpandInput> {
  for (let axisCount = 1; axisCount <= 3; axisCount++) {
    const combos = DOMAIN_POOL.length ** axisCount;
    for (let code = 0; code < combos; code++) {
      let n = code;
      const axes = [];
      for (let d = 0; d < axisCount; d++) {
        const domain = DOMAIN_POOL[n % DOMAIN_POOL.length];
        n = Math.floor(n / DOMAIN_POOL.length);
        axes.push({ name: `a${d}`, domain: domain as never });
      }
      const template = {
        t: axes.map((axis) => ({ $param: axis.name })),
      };
      for (const constraints of constraintsFor(axisCount)) {
        yield { template, axes, constraints };
        yield { template, axes, constraints, identity: ["$.t[0]"] };
      }
    }
  }
}

test("exhaustive small-domain completeness: streaming matches naive full product", () => {
  let checked = 0;
  for (const input of smallInputs()) {
    const result = verifyCompleteness(input);
    assert.ok(
      result.ok,
      `mismatch at rank ${result.mismatchAt} for ${JSON.stringify(input)} ` +
        `(streamed=${result.streamed} expected=${result.expected})`,
    );
    // Spot-check count and rank positioning against the same ground truth.
    const all = expand(input).records;
    assert.equal(count(input), all.length);
    if (all.length > 0) {
      assert.deepEqual(at(input, 0), all[0]);
      assert.deepEqual(at(input, -1), all[all.length - 1]);
      assert.deepEqual(atReverse(input, 0), all[all.length - 1]);
    }
    checked++;
  }
  assert.ok(checked > 1000, `expected broad coverage, checked ${checked}`);
});

test("verifyCompleteness refuses domains beyond the product limit", () => {
  const big: ExpandInput = {
    template: { x: { $param: "x" } },
    axes: [{ name: "x", domain: Array.from({ length: 1000 }, (_, i) => i) }],
  };
  assert.throws(() => verifyCompleteness(big, 100), /exceeds verification limit/);
});
