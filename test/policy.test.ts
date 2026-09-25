import assert from "node:assert/strict";
import test from "node:test";
import { and, cmp, inSet, or, resetIds } from "../src/ast.ts";
import { evaluatePolicy, validatePolicy, type Policy } from "../src/policy.ts";

function makePolicy(): Policy {
  resetIds();
  return {
    name: "employee-access",
    attributes: [
      { name: "age", min: 0, max: 120, required: true },
      { name: "dept", min: 100, max: 110, required: true },
      { name: "level", min: 1, max: 5, required: false },
    ],
    expr: or(
      and(cmp("age", ">=", 18), inSet("dept", [101, 102])),
      cmp("level", ">=", 4),
    ),
  };
}

test("evaluation reads ONLY explicitly declared attributes", () => {
  const policy = makePolicy();
  const touched: string[] = [];
  const context = new Proxy(
    { age: 30, dept: 101, ssn: "secret", role: "external" },
    {
      get(target, prop) {
        touched.push(String(prop));
        return Reflect.get(target, prop);
      },
    },
  );
  assert.equal(evaluatePolicy(policy, context), "true");
  assert.deepEqual([...new Set(touched)].sort(), ["age", "dept", "level"]);
});

test("missing optional attribute yields unknown, missing required attribute too", () => {
  const policy = makePolicy();
  assert.equal(evaluatePolicy(policy, { age: 30, dept: 999 }), "unknown"); // level missing decides
  assert.equal(evaluatePolicy(policy, { dept: 101 }), "unknown"); // age missing
});

test("non-integer attribute values are treated as missing", () => {
  const policy = makePolicy();
  assert.equal(evaluatePolicy(policy, { age: 30.5, dept: 101 }), "unknown");
  assert.equal(evaluatePolicy(policy, { age: 30, dept: 101, level: 5 }), "true");
});

test("validation rejects undeclared attributes and bad domains", () => {
  const policy = makePolicy();
  assert.deepEqual(validatePolicy(policy), []);
  const bad: Policy = { ...policy, expr: cmp("salary", ">", 1000) };
  assert.ok(validatePolicy(bad).some((e) => e.includes("undeclared attribute: salary")));
  const badDomain: Policy = {
    ...policy,
    attributes: [{ name: "x", min: 5, max: 1, required: true }],
  };
  assert.ok(validatePolicy(badDomain).some((e) => e.includes("invalid integer domain")));
  assert.throws(() => evaluatePolicy(bad, { salary: 1 }), /invalid policy/);
});
