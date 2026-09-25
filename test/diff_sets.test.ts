import assert from "node:assert/strict";
import test from "node:test";

import {
  ConditionFactory,
  contains,
  type DomainSpec,
} from "../src/index.ts";

const xy: DomainSpec = {
  x: { kind: "int", min: 0, max: 10 },
  y: { kind: "int", min: 0, max: 10 },
};

test("difference constraint: x-y<=3 does not contain x-y<=5; minimal cex x=4,y=0", () => {
  const F = new ConditionFactory();
  const res = contains(
    F,
    F.diff("x", "y", "<=", 3),
    F.diff("x", "y", "<=", 5),
    xy,
  );
  assert.equal(res.status, "not-contained");
  assert.deepEqual(
    (res as { counterexample: unknown }).counterexample,
    { x: 4, y: 0 },
  );
});

test("difference constraint: x-y<=5 contains x-y<=3", () => {
  const F = new ConditionFactory();
  assert.equal(
    contains(F, F.diff("x", "y", "<=", 5), F.diff("x", "y", "<=", 3), xy)
      .status,
    "contained",
  );
});

test("difference constraint combined with boolean ops", () => {
  const F = new ConditionFactory();
  // outer: x - y <= 2 or y - x <= 2  (i.e. |x - y| <= 2)
  const outer = F.or([F.diff("x", "y", "<=", 2), F.diff("y", "x", "<=", 2)]);
  const inner = F.and([F.diff("x", "y", ">=", -1), F.diff("x", "y", "<=", 1)]);
  assert.equal(contains(F, outer, inner, xy).status, "contained");
  const res = contains(F, inner, outer, xy);
  assert.equal(res.status, "not-contained");
  // minimal point with |x-y| = 2 but not <= 1, in lexicographic order
  assert.deepEqual(
    (res as { counterexample: unknown }).counterexample,
    { x: 0, y: 2 },
  );
});

test("equality difference constraint", () => {
  const F = new ConditionFactory();
  const res = contains(F, F.diff("x", "y", "==", 0), F.true_(), xy);
  assert.equal(res.status, "not-contained");
  assert.deepEqual(
    (res as { counterexample: unknown }).counterexample,
    { x: 0, y: 1 },
  );
});

const perms: DomainSpec = {
  perms: {
    kind: "set",
    sets: [[], ["read"], ["write"], ["read", "write"]],
  },
};

test("set predicate: has-read does not contain subset-of-{read}; cex is empty set", () => {
  const F = new ConditionFactory();
  const res = contains(
    F,
    F.setHas("perms", "read"),
    F.setSubset("perms", ["read"]),
    perms,
  );
  assert.equal(res.status, "not-contained");
  assert.deepEqual(
    (res as { counterexample: unknown }).counterexample,
    { perms: [] },
  );
});

test("set predicate: superset condition contains has-read", () => {
  const F = new ConditionFactory();
  const outer = F.setIntersects("perms", ["read", "write"]);
  const inner = F.setHas("perms", "read");
  assert.equal(contains(F, outer, inner, perms).status, "contained");
});

test("set predicates combined with enums", () => {
  const F = new ConditionFactory();
  const domain: DomainSpec = {
    ...perms,
    role: { kind: "enum", values: ["admin", "user"] },
  };
  const outer = F.or([
    F.in("role", ["admin"]),
    F.setHas("perms", "write"),
  ]);
  const inner = F.and([F.in("role", ["user"]), F.setHas("perms", "write")]);
  assert.equal(contains(F, outer, inner, domain).status, "contained");
});

test("empty set domain is contained in anything", () => {
  const F = new ConditionFactory();
  const empty: DomainSpec = { perms: { kind: "set", sets: [] } };
  assert.equal(
    contains(F, F.setHas("perms", "read"), F.setHas("perms", "write"), empty)
      .status,
    "contained",
  );
});
