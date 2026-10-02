import { test } from "node:test";
import assert from "node:assert/strict";
import { SchemaError, validate } from "../src/index.ts";

test("valid instance passes", () => {
  const r = validate(
    { price: "0.3", qty: 2, tags: ["a"] },
    {
      type: "object",
      required: ["price"],
      properties: {
        price: { type: "number", multipleOf: "0.1", minimum: "0" },
        qty: { type: "integer" },
        tags: { type: "array", items: { type: "string" }, maxItems: 3 },
      },
    },
  );
  assert.deepEqual(r.errors, []);
  assert.equal(r.valid, true);
});

test("errors carry instance path and schema path", () => {
  const r = validate(
    { price: "0.30000000000000004" },
    { properties: { price: { precision: 3, multipleOf: "0.1" } } },
  );
  assert.equal(r.valid, false);
  const prec = r.errors.find((e) => e.schemaPath.endsWith("/precision"));
  const mult = r.errors.find((e) => e.schemaPath.endsWith("/multipleOf"));
  assert.ok(prec && mult);
  assert.equal(prec.instancePath, "/price");
  assert.equal(prec.schemaPath, "#/properties/price/precision");
  assert.equal(mult.instancePath, "/price");
  // 0.30000000000000004 is NOT a multiple of 0.1 exactly
  assert.match(mult.message, /not an integer multiple/);
});

test("nested and array paths", () => {
  const r = validate(
    { items: [{ v: "1.5" }, { v: "2.5" }] },
    { properties: { items: { items: { properties: { v: { type: "integer" } } } } } },
  );
  assert.equal(r.errors.length, 2);
  assert.equal(r.errors[0]!.instancePath, "/items/0/v");
  assert.equal(r.errors[1]!.instancePath, "/items/1/v");
  assert.equal(r.errors[0]!.schemaPath, "#/properties/items/items/properties/v/type");
});

test("integer type accepts 1e3 and 2.0 by numeric value", () => {
  assert.equal(validate("1e3", { type: "integer" }).valid, true);
  assert.equal(validate("2.0", { type: "integer" }).valid, true);
  assert.equal(validate("2.5", { type: "integer" }).valid, false);
});

test("negative zero satisfies minimum 0", () => {
  assert.equal(validate("-0", { minimum: "0" }).valid, true);
  assert.equal(validate(-0, { exclusiveMinimum: "-0.1" }).valid, true);
});

test("unsupported keywords are schema errors, not silently ignored", () => {
  assert.throws(() => validate(1, { patternProperties: {} }), SchemaError);
  assert.throws(
    () => validate(1, { patternProperties: {} }),
    /only implements/,
  );
});

test("required and enum/const", () => {
  const r = validate({}, { required: ["a"] });
  assert.equal(r.valid, false);
  assert.equal(r.errors[0]!.schemaPath, "#/required");
  assert.equal(validate(1e3, { const: 1000 }).valid, true); // numeric equality is exact
  assert.equal(validate("a", { enum: ["a", "b"] }).valid, true);
  assert.equal(validate("c", { enum: ["a", "b"] }).valid, false);
});
