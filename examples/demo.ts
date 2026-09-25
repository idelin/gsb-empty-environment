/** Runnable samples: node examples/demo.ts (requires Node >= 22.18). */

import {
  ConditionFactory,
  contains,
  condToString,
  exhaustiveContains,
  verifyCounterexample,
  type ContainsResult,
  type DomainSpec,
} from "../src/index.ts";

const F = new ConditionFactory();

function show(
  title: string,
  domain: DomainSpec,
  outer: Parameters<typeof condToString>[0],
  inner: Parameters<typeof condToString>[0],
  opts?: { budget?: number },
): ContainsResult {
  const res = contains(F, outer, inner, domain, opts);
  console.log(`\n== ${title}`);
  console.log(`   outer: ${condToString(outer)}`);
  console.log(`   inner: ${condToString(inner)}`);
  console.log(`   verdict: ${res.status}`);
  if (res.status === "not-contained") {
    console.log(`   minimal counterexample: ${JSON.stringify(res.counterexample)}`);
    const check = verifyCounterexample(outer, inner, domain, res.counterexample);
    console.log(`   counterexample verified by exhaustive checker: ${check.ok}`);
  }
  if (res.status === "unknown") {
    console.log(`   cannot prove: attributes may be unknown -> ${res.attributes.join(", ")}`);
  }
  if (res.status === "undecided") {
    console.log(`   budget exhausted after ${res.stepsUsed} steps`);
  }
  try {
    const cross = exhaustiveContains(outer, inner, domain);
    console.log(`   exhaustive cross-check: ${cross.status}`);
  } catch (e) {
    console.log(`   exhaustive cross-check: skipped (${(e as Error).message})`);
  }
  return res;
}

// 1. x >= 3 does NOT contain x >= 2; minimal counterexample is x = 2.
show(
  "x>=3 contains x>=2 ?",
  { x: { kind: "int", min: 0, max: 10 } },
  F.cmp("x", ">=", 3),
  F.cmp("x", ">=", 2),
);

// 2. The other direction holds.
show(
  "x>=2 contains x>=3 ?",
  { x: { kind: "int", min: 0, max: 10 } },
  F.cmp("x", ">=", 2),
  F.cmp("x", ">=", 3),
);

// 3. An empty range is contained in any range (vacuous truth).
show(
  "empty range contained in anything",
  { x: { kind: "int", min: 5, max: 3 } }, // empty: min > max
  F.cmp("x", ">=", 100),
  F.cmp("x", "<=", -100),
);

// 4. Unknown-able attribute domain: containment cannot be proven.
show(
  "unknown attribute domain",
  { x: { kind: "int", min: 0, max: 10, optional: true } },
  F.cmp("x", ">=", 2),
  F.cmp("x", ">=", 3),
);

// 5. Enum + boolean combination (and / or / not).
show(
  "enum + boolean combination",
  {
    role: { kind: "enum", values: ["admin", "editor", "viewer"] },
    x: { kind: "int", min: 0, max: 10 },
  },
  F.or([
    F.in("role", ["admin"]),
    F.and([F.in("role", ["editor"]), F.cmp("x", ">=", 5)]),
  ]),
  F.and([F.in("role", ["admin", "editor"]), F.cmp("x", ">=", 5)]),
);

// 6. Difference constraints over two integer variables.
show(
  "difference constraint: x-y<=3 contains x-y<=5 ?",
  { x: { kind: "int", min: 0, max: 10 }, y: { kind: "int", min: 0, max: 10 } },
  F.diff("x", "y", "<=", 3),
  F.diff("x", "y", "<=", 5),
);

// 7. Set predicates.
show(
  "set predicate: perms has 'read' contains perms subsetOf {read}?",
  {
    perms: {
      kind: "set",
      sets: [[], ["read"], ["write"], ["read", "write"]],
    },
  },
  F.setHas("perms", "read"),
  F.setSubset("perms", ["read"]),
);

// 8. Budget interruption returns "undecided".
show(
  "tiny budget -> undecided",
  { x: { kind: "int", min: 0, max: 1_000_000 } },
  F.cmp("x", ">=", 3),
  F.cmp("x", ">=", 2),
  { budget: 2 },
);

console.log(`\nshared decision nodes allocated: ${F.nodeCount}`);
