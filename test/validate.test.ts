import { test } from "node:test";
import assert from "node:assert/strict";
import { SchemaError } from "../src/schema.ts";
import { contains, validate } from "../src/validate.ts";

test("range, multiple and digit constraints validate exactly", () => {
  const schema = {
    minimum: "0.1",
    maximum: "9.99",
    multipleOf: "0.1",
    maxTotalDigits: 3,
    maxFractionDigits: 2,
  };
  assert.equal(contains(schema, "0.3"), true);
  assert.equal(contains(schema, "9.9"), true);
  assert.equal(contains(schema, "0.35"), false); // not a multiple of 0.1
  assert.equal(contains(schema, "0.05"), false); // below minimum
  assert.equal(contains(schema, "10"), false); // above maximum
});

test("errors carry both instance path and rule path", () => {
  const result = validate("0.05", { minimum: "0.1" }, { instancePath: "/price" });
  assert.equal(result.valid, false);
  assert.deepEqual(result.errors.map((e) => [e.instancePath, e.rulePath]), [["/price", "/minimum"]]);
  const multi = validate("12.345", { maximum: "10", maxFractionDigits: 2 });
  assert.deepEqual(
    multi.errors.map((e) => e.rulePath).sort(),
    ["/maxFractionDigits", "/maximum"],
  );
});

test("exclusive bounds are exact, including at the boundary", () => {
  const schema = { exclusiveMinimum: "-1.5", exclusiveMaximum: "1.5" };
  assert.equal(contains(schema, "-1.5"), false);
  assert.equal(contains(schema, "1.5"), false);
  assert.equal(contains(schema, "-1.4999"), true);
  assert.equal(contains(schema, "0"), true);
});

test("1e3 counts 4 total digits by value, not 3 by text", () => {
  assert.equal(contains({ maxTotalDigits: 4 }, "1e3"), true);
  assert.equal(contains({ maxTotalDigits: 3 }, "1e3"), false);
  const res = validate("1e3", { maxTotalDigits: 3 });
  assert.equal(res.errors[0]!.rulePath, "/maxTotalDigits");
});

test("negative zero satisfies minimum 0 and keeps its literal", () => {
  assert.equal(contains({ minimum: "0" }, "-0"), true);
  assert.equal(contains({ minimum: "0" }, "-0.0e5"), true);
});

test("scientific bounds and negative multiples work without floats", () => {
  assert.equal(contains({ minimum: "-2.5e-4", maximum: "2.5E-4" }, "-0.00025"), true);
  assert.equal(contains({ multipleOf: "-0.25" }, "0.75"), true);
  assert.equal(contains({ multipleOf: "2e2" }, "1e3"), true);
  assert.equal(contains({ multipleOf: "2e2" }, "1.5e2"), false);
});

test("unsupported keywords are rejected, not silently ignored", () => {
  assert.throws(() => validate("1", { pattern: "^1$" } as never), (err: unknown) => {
    assert.ok(err instanceof SchemaError);
    assert.equal((err as SchemaError).rulePath, "/pattern");
    return true;
  });
});

test("schema errors: zero multipleOf, bad literal, oversized digit caps", () => {
  assert.throws(() => validate("1", { multipleOf: "0" }), SchemaError);
  assert.throws(() => validate("1", { minimum: "abc" }), SchemaError);
  assert.throws(() => validate("1", { maxTotalDigits: 10_000_000 }), SchemaError);
  assert.throws(() => validate("1", { maxFractionDigits: -1 }), SchemaError);
});

test("unparseable or over-budget instances are decimal errors", () => {
  const bad = validate("1.2.3", {});
  assert.equal(bad.valid, false);
  assert.equal(bad.errors[0]!.rulePath, "/type");
  const huge = validate("1e999999999999", {});
  assert.equal(huge.valid, false);
  assert.match(huge.errors[0]!.message, /budget/);
});
