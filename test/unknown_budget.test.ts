import assert from "node:assert/strict";
import test from "node:test";

import {
  ConditionFactory,
  contains,
  evalPoint,
  exhaustiveContains,
  UNKNOWN,
  type DomainSpec,
} from "../src/index.ts";

test("three-valued point evaluation (Kleene semantics)", () => {
  const F = new ConditionFactory();
  const ge2 = F.cmp("x", ">=", 2);
  const lt5 = F.cmp("x", "<", 5);
  assert.equal(evalPoint(ge2, { x: UNKNOWN }), "unknown");
  assert.equal(evalPoint(F.and([ge2, F.false_()]), { x: UNKNOWN }), "false");
  assert.equal(evalPoint(F.or([ge2, F.true_()]), { x: UNKNOWN }), "true");
  assert.equal(evalPoint(F.and([ge2, lt5]), { x: UNKNOWN }), "unknown");
  assert.equal(evalPoint(F.not(ge2), { x: UNKNOWN }), "unknown");
  assert.equal(evalPoint(ge2, { x: 3 }), "true");
  assert.equal(evalPoint(ge2, { x: 1 }), "false");
});

test("unknown-able attribute domain: containment cannot be proven", () => {
  const F = new ConditionFactory();
  const domain: DomainSpec = {
    x: { kind: "int", min: 0, max: 10, optional: true },
  };
  const res = contains(F, F.cmp("x", ">=", 2), F.cmp("x", ">=", 3), domain);
  assert.equal(res.status, "unknown");
  assert.deepEqual((res as { attributes: string[] }).attributes, ["x"]);
  // The independent exhaustive checker agrees.
  assert.equal(
    exhaustiveContains(F.cmp("x", ">=", 2), F.cmp("x", ">=", 3), domain)
      .status,
    "unknown",
  );
});

test("unknown optionality only matters for referenced attributes", () => {
  const F = new ConditionFactory();
  const domain: DomainSpec = {
    x: { kind: "int", min: 0, max: 10 },
    note: { kind: "enum", values: ["a", "b"], optional: true },
  };
  // "note" is optional but never read by the conditions.
  assert.equal(
    contains(F, F.cmp("x", ">=", 2), F.cmp("x", ">=", 3), domain).status,
    "contained",
  );
});

test("budget interruption returns undecided", () => {
  const F = new ConditionFactory();
  const domain: DomainSpec = { x: { kind: "int", min: 0, max: 1_000_000 } };
  const res = contains(F, F.cmp("x", ">=", 3), F.cmp("x", ">=", 2), domain, {
    budget: 2,
  });
  assert.equal(res.status, "undecided");
  assert.equal((res as { reason: string }).reason, "budget");
});

test("same query with ample budget resolves", () => {
  const F = new ConditionFactory();
  const domain: DomainSpec = { x: { kind: "int", min: 0, max: 1_000_000 } };
  const res = contains(F, F.cmp("x", ">=", 3), F.cmp("x", ">=", 2), domain, {
    budget: 10_000,
  });
  assert.equal(res.status, "not-contained");
  assert.deepEqual(
    (res as { counterexample: unknown }).counterexample,
    { x: 2 },
  );
});

test("interval splitting avoids per-value enumeration", () => {
  const F = new ConditionFactory();
  // A 10^12-point space; containment is decided with a handful of box
  // evaluations, far below the default budget.
  const domain: DomainSpec = {
    x: { kind: "int", min: 0, max: 1_000_000 },
    y: { kind: "int", min: 0, max: 1_000_000 },
  };
  const res = contains(
    F,
    F.diff("x", "y", "<=", 500_000),
    F.diff("x", "y", "<=", 400_000),
    domain,
    { budget: 1_000 },
  );
  assert.equal(res.status, "contained");
});
