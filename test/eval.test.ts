import assert from "node:assert/strict";
import { test } from "node:test";
import { and, inSet, intCmp, not, or } from "../src/conditions.ts";
import { evalCondition, evalRuleSet } from "../src/eval.ts";
import type { RuleSet } from "../src/types.ts";

test("finite-set membership only reads the named explicit attribute", () => {
  assert.equal(evalCondition(inSet("role", ["admin"]), { role: "admin" }), true);
  assert.equal(evalCondition(inSet("role", ["admin"]), { role: "viewer" }), false);
  assert.equal(evalCondition(inSet("role", ["admin"]), { other: "admin" }), false);
  assert.equal(
    evalCondition(inSet("role", ["admin"]), { role: "admin", extra: 1 }),
    true,
  );
});

test("integer comparisons with strict integer typing", () => {
  assert.equal(evalCondition(intCmp("age", ">=", 2), { age: 2 }), true);
  assert.equal(evalCondition(intCmp("age", ">=", 2), { age: 1 }), false);
  assert.equal(evalCondition(intCmp("age", "<", 2), { age: 1 }), true);
  assert.equal(evalCondition(intCmp("age", "=", 2), { age: 2 }), true);
  assert.equal(evalCondition(intCmp("age", "!=", 2), { age: 3 }), true);
  assert.equal(evalCondition(intCmp("age", ">=", 2), {}), false);
  assert.equal(evalCondition(intCmp("age", ">=", 2), { age: 2.5 }), false);
  assert.equal(evalCondition(intCmp("age", ">=", 2), { age: "2" }), false);
});

test("boolean AND / OR / NOT", () => {
  const c = and(
    inSet("role", ["editor", "admin"]),
    or(inSet("env", ["prod"]), not(inSet("mfa", [false]))),
  );
  assert.equal(evalCondition(c, { role: "editor", env: "prod", mfa: false }), true);
  assert.equal(evalCondition(c, { role: "editor", env: "dev", mfa: true }), true);
  assert.equal(evalCondition(c, { role: "editor", env: "dev", mfa: false }), false);
  assert.equal(evalCondition(c, { role: "guest", env: "prod", mfa: false }), false);
});

test("deny overrides allow independent of rule order", () => {
  const base = [
    { id: "a", effect: "allow" as const, when: inSet("role", ["admin"]) },
    { id: "d", effect: "deny" as const, when: inSet("role", ["admin"]) },
  ];
  const mk = (rules: typeof base): RuleSet => ({
    name: "p",
    defaultEffect: "deny",
    rules,
  });
  const input = { role: "admin" };
  const d1 = evalRuleSet(mk(base), input);
  const d2 = evalRuleSet(mk([...base].reverse()), input);
  assert.equal(d1.effect, "deny");
  assert.equal(d2.effect, "deny");
  assert.deepEqual([...d1.matchedRuleIds], ["d"]);
  assert.deepEqual([...d2.matchedRuleIds], ["d"]);
});

test("default effect applies when nothing matches; allow otherwise", () => {
  const rs: RuleSet = {
    name: "p",
    defaultEffect: "deny",
    rules: [{ id: "a", effect: "allow", when: inSet("role", ["admin"]) }],
  };
  assert.deepEqual(evalRuleSet(rs, { role: "admin" }), {
    effect: "allow",
    matchedRuleIds: ["a"],
    defaulted: false,
  });
  assert.deepEqual(evalRuleSet(rs, { role: "guest" }), {
    effect: "deny",
    matchedRuleIds: [],
    defaulted: true,
  });
});

test("evaluation touches no attribute other than those named in conditions", () => {
  // The get trap fails the test if any attribute other than `role` is
  // read. There is no external identity lookup: evaluation is a pure
  // function of the explicit attributes on the input.
  const input = new Proxy({ role: "admin" } as Record<string, unknown>, {
    get(target, prop) {
      if (typeof prop === "string") {
        assert.equal(prop, "role", `unexpected attribute read: ${prop}`);
      }
      return Reflect.get(target, prop);
    },
  });
  assert.equal(evalCondition(inSet("role", ["admin"]), input), true);
});
