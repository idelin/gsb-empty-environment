import assert from "node:assert/strict";
import test from "node:test";

import {
  ConditionFactory,
  contains,
  policyContains,
  verifyCounterexample,
  type DomainSpec,
} from "../src/index.ts";

const intDomain: DomainSpec = { x: { kind: "int", min: 0, max: 10 } };

test("x>=3 does not contain x>=2; minimal counterexample is x=2", () => {
  const F = new ConditionFactory();
  const res = contains(F, F.cmp("x", ">=", 3), F.cmp("x", ">=", 2), intDomain);
  assert.equal(res.status, "not-contained");
  assert.deepEqual(
    (res as { counterexample: unknown }).counterexample,
    { x: 2 },
  );
  const check = verifyCounterexample(
    F.cmp("x", ">=", 3),
    F.cmp("x", ">=", 2),
    intDomain,
    (res as { counterexample: Record<string, number> }).counterexample,
  );
  assert.equal(check.ok, true);
});

test("x>=2 contains x>=3", () => {
  const F = new ConditionFactory();
  const res = contains(F, F.cmp("x", ">=", 2), F.cmp("x", ">=", 3), intDomain);
  assert.equal(res.status, "contained");
});

test("every condition contains itself", () => {
  const F = new ConditionFactory();
  const c = F.and([F.cmp("x", ">=", 2), F.cmp("x", "<", 8)]);
  assert.equal(contains(F, c, c, intDomain).status, "contained");
});

test("empty int range is contained in any range", () => {
  const F = new ConditionFactory();
  const empty: DomainSpec = { x: { kind: "int", min: 5, max: 3 } };
  assert.equal(
    contains(F, F.cmp("x", ">=", 100), F.cmp("x", "<=", -100), empty).status,
    "contained",
  );
});

test("empty enum domain is contained in anything", () => {
  const F = new ConditionFactory();
  const empty: DomainSpec = { r: { kind: "enum", values: [] } };
  assert.equal(
    contains(F, F.in("r", ["a"]), F.in("r", ["b"]), empty).status,
    "contained",
  );
});

test("unsatisfiable inner condition is contained in anything", () => {
  const F = new ConditionFactory();
  const inner = F.and([F.cmp("x", ">=", 5), F.cmp("x", "<=", 3)]);
  assert.equal(
    contains(F, F.false_(), inner, intDomain).status,
    "contained",
  );
});

test("enum containment both directions", () => {
  const F = new ConditionFactory();
  const domain: DomainSpec = {
    role: { kind: "enum", values: ["admin", "editor", "viewer"] },
  };
  assert.equal(
    contains(F, F.in("role", ["admin", "editor"]), F.in("role", ["admin"]), domain)
      .status,
    "contained",
  );
  const res = contains(
    F,
    F.in("role", ["admin"]),
    F.in("role", ["admin", "editor"]),
    domain,
  );
  assert.equal(res.status, "not-contained");
  assert.deepEqual(
    (res as { counterexample: unknown }).counterexample,
    { role: "editor" },
  );
});

test("boolean combination: and / or / not", () => {
  const F = new ConditionFactory();
  const domain: DomainSpec = {
    role: { kind: "enum", values: ["admin", "editor", "viewer"] },
    x: { kind: "int", min: 0, max: 10 },
  };
  const outer = F.or([
    F.in("role", ["admin"]),
    F.and([F.in("role", ["editor"]), F.cmp("x", ">=", 5)]),
  ]);
  const inner = F.and([F.in("role", ["admin", "editor"]), F.cmp("x", ">=", 5)]);
  assert.equal(contains(F, outer, inner, domain).status, "contained");

  const notAdmin = F.not(F.in("role", ["admin"]));
  const res = contains(F, notAdmin, F.true_(), domain);
  assert.equal(res.status, "not-contained");
  // counterexample only mentions attributes the conditions actually read
  assert.deepEqual(
    (res as { counterexample: unknown }).counterexample,
    { role: "admin" },
  );
});

test("in() works on int attributes as finite membership", () => {
  const F = new ConditionFactory();
  const res = contains(F, F.in("x", [2, 4, 6]), F.cmp("x", ">=", 0), intDomain);
  assert.equal(res.status, "not-contained");
  assert.deepEqual(
    (res as { counterexample: unknown }).counterexample,
    { x: 0 },
  );
});

test("reading an undeclared attribute is rejected", () => {
  const F = new ConditionFactory();
  assert.throws(
    () => contains(F, F.cmp("z", ">=", 1), F.true_(), intDomain),
    /not declared in the domain/,
  );
});

test("kind mismatches are rejected", () => {
  const F = new ConditionFactory();
  const domain: DomainSpec = { r: { kind: "enum", values: ["a"] } };
  assert.throws(() => contains(F, F.cmp("r", ">=", 1), F.true_(), domain));
});

test("policyContains compares policy applicability ranges", () => {
  const F = new ConditionFactory();
  const p = { id: "p", condition: F.cmp("x", ">=", 3) };
  const q = { id: "q", condition: F.cmp("x", ">=", 2) };
  const res = policyContains(F, p, q, intDomain);
  assert.equal(res.status, "not-contained");
  assert.equal(policyContains(F, q, p, intDomain).status, "contained");
});

test("hash-consing shares identical sub-conditions", () => {
  const F = new ConditionFactory();
  const a = F.and([F.cmp("x", ">=", 2), F.cmp("x", "<=", 9)]);
  const b = F.and([F.cmp("x", "<=", 9), F.cmp("x", ">=", 2)]);
  assert.equal(F.id(a), F.id(b));
  assert.equal(F.id(F.not(F.not(a))), F.id(a));
});
