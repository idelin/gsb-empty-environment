import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { allowAll, and, attrIn, denyAll, evalPolicy, intCmp, not, or, validatePolicy } from "../src/policy.ts";
import type { Attributes, Policy } from "../src/types.ts";

describe("policy evaluation reads only explicit attributes", () => {
  it("allowAll / denyAll", () => {
    assert.equal(evalPolicy(allowAll(), {}), true);
    assert.equal(evalPolicy(denyAll(), { anything: true }), false);
  });

  it("attrIn checks membership in a finite set", () => {
    const policy = attrIn("env", ["prod", "staging"]);
    assert.equal(evalPolicy(policy, { env: "prod" }), true);
    assert.equal(evalPolicy(policy, { env: "dev" }), false);
  });

  it("attrIn supports array-valued attributes and fails closed", () => {
    const policy = attrIn("role", ["admin", "auditor"]);
    assert.equal(evalPolicy(policy, { role: ["reader", "auditor"] }), true);
    assert.equal(evalPolicy(policy, {}), false);
    assert.equal(evalPolicy(policy, { role: 42 }), false);
  });

  it("does not read inherited properties", () => {
    const attrs = Object.create({ injected: "prod" }) as Attributes;
    assert.equal(evalPolicy(attrIn("injected", ["prod"]), attrs), false);
  });

  it("intCmp supports all integer comparisons", () => {
    assert.equal(evalPolicy(intCmp("level", ">=", 3), { level: 3 }), true);
    assert.equal(evalPolicy(intCmp("level", ">", 3), { level: 3 }), false);
    assert.equal(evalPolicy(intCmp("level", "<", 3), { level: 2 }), true);
    assert.equal(evalPolicy(intCmp("level", "<=", 3), { level: 3 }), true);
    assert.equal(evalPolicy(intCmp("level", "==", 3), { level: 3 }), true);
    assert.equal(evalPolicy(intCmp("level", "!=", 3), { level: 3 }), false);
  });

  it("intCmp rejects non-integer values on either side", () => {
    assert.equal(evalPolicy(intCmp("level", ">=", 3), { level: 3.5 }), false);
    assert.equal(evalPolicy(intCmp("level", ">=", 3), { level: "3" }), false);
    assert.equal(evalPolicy(intCmp("level", ">=", 3), {}), false);
  });

  it("boolean operators compose", () => {
    const policy = and(attrIn("env", ["prod"]), or(intCmp("level", ">=", 3), attrIn("role", ["admin"])), not(attrIn("blocked", [true])));
    assert.equal(evalPolicy(policy, { env: "prod", level: 4, blocked: false }), true);
    assert.equal(evalPolicy(policy, { env: "prod", level: 1, blocked: false }), false);
    assert.equal(evalPolicy(policy, { env: "prod", level: 5, blocked: true }), false);
  });

  it("short-circuits conjunction with denyAll", () => {
    assert.equal(and(allowAll(), denyAll()).kind, "denyAll");
    assert.equal(or(denyAll(), allowAll()).kind, "allowAll");
  });
});

describe("policy structural validation", () => {
  it("rejects empty finite sets", () => {
    assert.throws(() => validatePolicy({ kind: "attrIn", attr: "env", values: [] }), /non-empty/);
  });

  it("rejects bad integer operator and non-integer literal", () => {
    assert.throws(() => validatePolicy({ kind: "intCmp", attr: "x", op: "~" as "==", value: 1 }), /operator/);
    assert.throws(() => validatePolicy(intCmp("x", "==", 1.5)), /integer/);
  });

  it("rejects empty boolean compounds and unknown kinds", () => {
    assert.throws(() => validatePolicy({ kind: "and", operands: [] }), /at least one/);
    assert.throws(() => validatePolicy({ kind: "mystery" } as unknown as Policy), /unknown policy/);
  });
});
