import test from "node:test";
import assert from "node:assert/strict";
import { PolicyError, evalCond, validateCond, type Cond } from "../src/policy.ts";

const allowed = new Set(["dept", "clearance", "active"]);

test("finite-set membership matches only listed values", () => {
  const c: Cond = { op: "in", attr: "dept", values: ["eng", "fin"] };
  assert.equal(evalCond(c, { dept: "eng" }), true);
  assert.equal(evalCond(c, { dept: "ops" }), false);
  assert.equal(evalCond(c, {}), false); // missing attribute -> false, no throw
});

test("integer conditions require safe integers at runtime", () => {
  const ge2: Cond = { op: "int", attr: "clearance", cmp: "gte", value: 2 };
  assert.equal(evalCond(ge2, { clearance: 3 }), true);
  assert.equal(evalCond(ge2, { clearance: 1 }), false);
  assert.equal(evalCond(ge2, { clearance: 2.5 }), false); // non-integer -> false
  assert.equal(evalCond(ge2, { clearance: "9" as unknown as number }), false);
  assert.equal(evalCond(ge2, {}), false);
});

test("between is inclusive and validated", () => {
  const c: Cond = { op: "int", attr: "clearance", cmp: "between", value: 1, max: 3 };
  assert.equal(evalCond(c, { clearance: 1 }), true);
  assert.equal(evalCond(c, { clearance: 3 }), true);
  assert.equal(evalCond(c, { clearance: 4 }), false);
  assert.throws(
    () => validateCond({ op: "int", attr: "clearance", cmp: "between", value: 3, max: 1 }, allowed),
    PolicyError,
  );
});

test("boolean combinators compose", () => {
  const c: Cond = {
    op: "and",
    args: [
      { op: "in", attr: "active", values: [true] },
      { op: "or", args: [
        { op: "in", attr: "dept", values: ["eng"] },
        { op: "not", arg: { op: "int", attr: "clearance", cmp: "lt", value: 2 } },
      ]},
    ],
  };
  assert.equal(evalCond(c, { active: true, dept: "eng", clearance: 0 }), true);
  assert.equal(evalCond(c, { active: true, dept: "fin", clearance: 5 }), true);
  assert.equal(evalCond(c, { active: true, dept: "fin", clearance: 0 }), false);
  assert.equal(evalCond(c, { active: false, dept: "eng", clearance: 9 }), false);
});

test("policy may only reference declared attributes", () => {
  assert.throws(
    () => validateCond({ op: "in", attr: "password", values: ["x"] }, allowed),
    /undeclared attribute: password/,
  );
});

test("sets must be finite and scalar", () => {
  assert.throws(
    () => validateCond({ op: "in", attr: "dept", values: [{ nested: 1 } as unknown as string] }, allowed),
    PolicyError,
  );
  assert.throws(
    () => validateCond({ op: "in", attr: "dept", values: "eng" as unknown as string[] }, allowed),
    PolicyError,
  );
});

test("integer bounds must be safe integers", () => {
  assert.throws(
    () => validateCond({ op: "int", attr: "clearance", cmp: "eq", value: 1.5 }, allowed),
    PolicyError,
  );
  assert.throws(
    () => validateCond({ op: "int", attr: "clearance", cmp: "eq", value: Number.MAX_SAFE_INTEGER + 1 }, allowed),
    PolicyError,
  );
});

test("error messages name attributes, never values", () => {
  const secret = "s3cr3t-value";
  try {
    validateCond({ op: "in", attr: "token", values: [secret] }, allowed);
    assert.fail("should throw");
  } catch (e) {
    assert.ok(e instanceof PolicyError);
    assert.ok(!e.message.includes(secret));
    assert.ok(e.message.includes("token"));
  }
});
