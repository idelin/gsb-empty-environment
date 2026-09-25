import assert from "node:assert/strict";
import { test } from "node:test";
import { and, inSet, intCmp, or } from "../src/conditions.ts";
import { compareRuleSets, type Region } from "../src/compare.ts";
import { domainSize, enumerateInputs, projectRegion } from "../src/domain.ts";
import { evalRuleSet } from "../src/eval.ts";
import type { DomainSpec, Input, RuleSet } from "../src/types.ts";

const smallDomain: DomainSpec = {
  role: { kind: "set", values: ["guest", "editor", "admin"] },
  env: { kind: "set", values: ["dev", "prod"] },
  age: { kind: "intRange", min: 0, max: 2 },
};

const mk = (rules: RuleSet["rules"], defaultEffect: "allow" | "deny" = "deny"): RuleSet => ({
  name: "p",
  defaultEffect,
  rules,
});

const counts = (rs: { regions: Region[] }, kind: Region["changeType"]) =>
  rs.regions.filter((r) => r.changeType === kind).reduce((n, r) => n + r.count, 0);

test("enumeration covers exactly the declared finite cartesian product", () => {
  assert.equal(domainSize(smallDomain), 3 * 2 * 3);
  assert.equal(enumerateInputs(smallDomain).length, 18);
});

test("identical rule sets produce a single fully-unchanged partition", () => {
  const rs = mk([{ id: "a", effect: "allow", when: inSet("role", ["admin"]) }]);
  const result = compareRuleSets(rs, rs, smallDomain);
  assert.equal(result.totalInputs, 18);
  assert.equal(counts(result, "unchanged"), 18);
  assert.equal(result.summary.allowToDeny + result.summary.denyToAllow, 0);
  assert.equal(result.summary.explanationOnly, 0);
  // every region in this comparison is unchanged
  assert.ok(result.regions.every((r) => r.changeType === "unchanged"));
});

test("reordering rules changes neither decisions nor regions", () => {
  const oldRs = mk([
    { id: "a1", effect: "allow", when: inSet("role", ["admin"]) },
    { id: "d1", effect: "deny", when: and(inSet("env", ["prod"]), inSet("role", ["editor"])) },
    { id: "a2", effect: "allow", when: inSet("env", ["dev"]) },
  ]);
  const shuffled = mk([...oldRs.rules].reverse());
  const result = compareRuleSets(oldRs, shuffled, smallDomain);
  assert.equal(counts(result, "unchanged"), 18);
  assert.ok(result.regions.every((r) => r.changeType === "unchanged"));
});

test("commuting AND/OR operands changes neither decisions nor explanations", () => {
  const oldRs = mk([
    { id: "r", effect: "allow", when: and(inSet("role", ["admin"]), inSet("env", ["prod"])) },
  ]);
  const commute = mk([
    { id: "r", effect: "allow", when: and(inSet("env", ["prod"]), inSet("role", ["admin"])) },
  ]);
  const orOld = mk([
    { id: "r", effect: "allow", when: or(inSet("role", ["admin"]), inSet("age", [0])) },
  ]);
  const orNew = mk([
    { id: "r", effect: "allow", when: or(inSet("age", [0]), inSet("role", ["admin"])) },
  ]);
  assert.equal(counts(compareRuleSets(oldRs, commute, smallDomain), "unchanged"), 18);
  assert.equal(counts(compareRuleSets(orOld, orNew, smallDomain), "unchanged"), 18);
});

test("allow -> deny is listed separately from deny -> allow", () => {
  const oldRs = mk([
    { id: "allow-admins", effect: "allow", when: inSet("role", ["admin", "editor"]) },
  ]);
  const newRs = mk([
    {
      id: "deny-everyone-except-admin",
      effect: "deny",
      when: inSet("role", ["editor", "guest"]),
    },
    { id: "allow-admins", effect: "allow", when: inSet("role", ["admin"]) },
  ], "deny");
  const result = compareRuleSets(oldRs, newRs, smallDomain);

  // editor: was allowed, now denied (2 envs * 3 ages = 6 points)
  assert.equal(result.summary.allowToDeny, 6);
  assert.equal(result.summary.denyToAllow, 0);
  const region = result.regions.find((r) => r.direction === "allow->deny");
  assert.ok(region);
  assert.deepEqual(region!.sample.role, "editor");
  assert.equal(region!.oldEffect, "allow");
  assert.equal(region!.newEffect, "deny");
});

test("deny -> allow from relaxing an integer threshold", () => {
  const oldRs = mk([
    { id: "allow", effect: "allow", when: inSet("role", ["admin"]) },
    { id: "agegate", effect: "deny", when: intCmp("age", ">=", 1) },
  ]);
  const newRs = mk([
    { id: "allow", effect: "allow", when: inSet("role", ["admin"]) },
    { id: "agegate", effect: "deny", when: intCmp("age", ">=", 2) },
  ]);
  const result = compareRuleSets(oldRs, newRs, smallDomain);
  // admin @ age 1, env in {dev, prod} flips deny -> allow (2 points)
  assert.equal(result.summary.denyToAllow, 2);
  assert.equal(result.summary.allowToDeny, 0);
  const flipped = result.regions.find((r) => r.direction === "deny->allow");
  assert.ok(flipped);
  assert.equal(flipped!.sample.role, "admin");
  assert.equal(flipped!.sample.age, 1);
});

test("renaming a rule is explanation-only, never a decision change", () => {
  const oldRs = mk([{ id: "old-name", effect: "allow", when: inSet("role", ["admin"]) }]);
  const newRs = mk([{ id: "new-name", effect: "allow", when: inSet("role", ["admin"]) }]);
  const result = compareRuleSets(oldRs, newRs, smallDomain);
  assert.equal(result.summary.allowToDeny + result.summary.denyToAllow, 0);
  assert.equal(counts(result, "explanation-only"), 6); // admin: 2 envs * 3 ages
  const region = result.regions.find((r) => r.changeType === "explanation-only");
  assert.deepEqual([...region!.oldDecision.matchedRuleIds], ["old-name"]);
  assert.deepEqual([...region!.newDecision.matchedRuleIds], ["new-name"]);
  assert.equal(region!.oldEffect, "allow");
});

test("splitting one rule into disjoint rules is explanation-only", () => {
  const oldRs = mk([
    { id: "one", effect: "allow", when: inSet("role", ["editor", "admin"]) },
  ]);
  const split = mk([
    { id: "part-a", effect: "allow", when: inSet("role", ["editor"]) },
    { id: "part-b", effect: "allow", when: inSet("role", ["admin"]) },
  ]);
  const result = compareRuleSets(oldRs, split, smallDomain);
  assert.equal(counts(result, "explanation-only"), 12);
  assert.equal(result.summary.allowToDeny + result.summary.denyToAllow, 0);
});

test("explicit new deny that matches a previously default-denied input is explanation-only", () => {
  const oldRs = mk([]); // default deny
  const newRs = mk([
    { id: "explicit", effect: "deny", when: inSet("role", ["guest"]) },
  ]);
  const result = compareRuleSets(oldRs, newRs, smallDomain);
  assert.equal(counts(result, "explanation-only"), 6); // guest: 2 envs * 3 ages
  assert.equal(counts(result, "unchanged"), 12);
});

test("region projection reports only the constrained attributes", () => {
  const oldRs = mk([]);
  const newRs = mk([
    { id: "a", effect: "allow", when: and(inSet("role", ["admin"]), inSet("env", ["prod"])) },
  ]);
  const result = compareRuleSets(oldRs, newRs, smallDomain);
  const region = result.regions.find((r) => r.direction === "deny->allow");
  assert.ok(region);
  assert.equal(region!.count, 3); // ages 0..2
  const byAttr = new Map(region!.projections.map((p) => [p.attr, p]));
  assert.deepEqual([...byAttr.get("role")!.values], ["admin"]);
  assert.deepEqual([...byAttr.get("env")!.values], ["prod"]);
  assert.equal(byAttr.get("age")!.spansAll, true);
});

test("every region's representative sample actually has the reported decisions", () => {
  const oldRs = mk([
    { id: "a", effect: "allow", when: inSet("role", ["admin", "editor"]) },
  ]);
  const newRs = mk([{ id: "a", effect: "allow", when: inSet("role", ["admin"]) }]);
  const result = compareRuleSets(oldRs, newRs, smallDomain);
  for (const region of result.regions) {
    assert.equal(evalRuleSet(oldRs, region.sample as Input).effect, region.oldEffect);
    assert.equal(evalRuleSet(newRs, region.sample as Input).effect, region.newEffect);
  }
});

test("point counts over all regions sum to the finite domain size", () => {
  const oldRs = mk([
    { id: "a", effect: "allow", when: inSet("role", ["admin", "editor"]) },
  ]);
  const newRs = mk([{ id: "a", effect: "allow", when: inSet("role", ["admin"]) }]);
  const result = compareRuleSets(oldRs, newRs, smallDomain);
  const sum = result.regions.reduce((n, r) => n + r.count, 0);
  assert.equal(sum, result.totalInputs);
});

test("projectRegion marks span correctly", () => {
  const points: Input[] = [
    { role: "admin", env: "prod", age: 0 },
    { role: "admin", env: "prod", age: 1 },
  ];
  const proj = new Map(projectRegion(points, smallDomain).map((p) => [p.attr, p]));
  assert.equal(proj.get("role")!.spansAll, false);
  assert.equal(proj.get("env")!.spansAll, false);
  assert.equal(proj.get("age")!.spansAll, false);
});
