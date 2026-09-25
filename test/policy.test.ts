import { test } from "node:test";
import assert from "node:assert/strict";
import { compilePolicy } from "../src/policy.ts";
import type { PolicySpec } from "../src/policy.ts";
import { SPEC } from "./fixture.ts";

function withSpec(patch: Partial<PolicySpec>): PolicySpec {
  return { ...SPEC, ...patch };
}

test("compiles a valid policy and derives maskable fields", () => {
  const policy = compilePolicy(SPEC);
  assert.deepEqual([...policy.maskableFields].sort(), ["note", "salary"]);
  assert.deepEqual(policy.publicFields, SPEC.publicFields);
});

test("rejects conditions referencing undeclared attributes", () => {
  assert.throws(
    () =>
      compilePolicy(
        withSpec({
          rowVisibleWhen: { op: "in", attribute: "subject.team", values: ["eng"] },
        }),
      ),
    /unknown attribute "subject\.team"/,
  );
});

test("rejects malformed attribute references", () => {
  assert.throws(
    () =>
      compilePolicy(
        withSpec({
          rowVisibleWhen: { op: "int", attribute: "clearance", cmp: "gt", value: 1 },
        }),
      ),
    /malformed attribute reference/,
  );
});

test("rejects in-conditions on integer attributes", () => {
  assert.throws(
    () =>
      compilePolicy(
        withSpec({
          rowVisibleWhen: { op: "in", attribute: "subject.clearance", values: ["1"] },
        }),
      ),
    /requires an enum attribute/,
  );
});

test("rejects in-values outside the declared finite set", () => {
  assert.throws(
    () =>
      compilePolicy(
        withSpec({
          rowVisibleWhen: { op: "in", attribute: "subject.role", values: ["superuser"] },
        }),
      ),
    /outside the declared finite set/,
  );
});

test("rejects non-integer literals in integer conditions", () => {
  assert.throws(
    () =>
      compilePolicy(
        withSpec({
          rowVisibleWhen: { op: "int", attribute: "subject.clearance", cmp: "gt", value: 1.5 },
        }),
      ),
    /requires an integer literal/,
  );
});

test("rejects integer conditions on enum attributes", () => {
  assert.throws(
    () =>
      compilePolicy(
        withSpec({
          rowVisibleWhen: { op: "int", attribute: "row.region", cmp: "eq", value: 1 },
        }),
      ),
    /requires an integer attribute/,
  );
});

test("rejects cross-attribute comparison with a non-integer attribute", () => {
  assert.throws(
    () =>
      compilePolicy(
        withSpec({
          rowVisibleWhen: {
            op: "int",
            attribute: "subject.clearance",
            cmp: "eq",
            otherAttribute: "row.region",
          },
        }),
      ),
    /requires an integer attribute/,
  );
});

test("rejects empty boolean combinators", () => {
  assert.throws(
    () => compilePolicy(withSpec({ rowVisibleWhen: { op: "and", all: [] } })),
    /requires at least one operand/,
  );
});

test("rejects mask rules targeting fields outside the public schema", () => {
  assert.throws(
    () =>
      compilePolicy(
        withSpec({
          maskRules: [
            { id: "bad", when: { op: "in", attribute: "subject.role", values: ["hr"] }, fields: ["password"] },
          ],
        }),
      ),
    /not in the public schema/,
  );
});

test("rejects duplicate mask rule ids", () => {
  const rule = SPEC.maskRules[0];
  assert.throws(
    () => compilePolicy(withSpec({ maskRules: [rule, rule] })),
    /duplicate mask rule id/,
  );
});
