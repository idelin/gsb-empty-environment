import { test } from "node:test";
import assert from "node:assert/strict";
import { validate } from "../src/validator.ts";
import type { Schema } from "../src/schema.ts";

test("valid instance yields no errors", () => {
  const schema: Schema = {
    type: "object",
    properties: { age: { type: "integer", minimum: 0 } },
    required: ["age"],
  };
  assert.deepEqual(validate({ age: 3 }, schema), []);
});

test("nested numeric error carries instance path and rule path", () => {
  const schema: Schema = {
    type: "object",
    properties: { age: { type: "integer", minimum: 0 } },
  };
  const errors = validate({ age: -1 }, schema);
  assert.equal(errors.length, 1);
  assert.equal(errors[0].instancePath, "/age");
  assert.equal(errors[0].schemaPath, "/properties/age/minimum");
});

test("array item error path", () => {
  const schema: Schema = { type: "array", items: { type: "string" } };
  const errors = validate(["a", 2], schema);
  assert.equal(errors.length, 1);
  assert.equal(errors[0].instancePath, "/1");
  assert.equal(errors[0].schemaPath, "/items/type");
});

test("required and additionalProperties", () => {
  const schema: Schema = {
    type: "object",
    properties: { a: { type: "string" } },
    required: ["a"],
    additionalProperties: false,
  };
  const missing = validate({}, schema);
  assert.equal(missing.length, 1);
  assert.equal(missing[0].schemaPath, "/required");
  const extra = validate({ a: "x", b: 1 }, schema);
  assert.equal(extra.length, 1);
  assert.equal(extra[0].schemaPath, "/additionalProperties");
});

test("pattern, enum, multipleOf, exclusive bounds", () => {
  assert.equal(validate("abc", { pattern: "^[0-9]+$" })[0].schemaPath, "/pattern");
  assert.equal(validate("x", { enum: ["a", "b"] })[0].schemaPath, "/enum");
  assert.equal(validate(7, { multipleOf: 2 })[0].schemaPath, "/multipleOf");
  assert.equal(validate(5, { exclusiveMinimum: 5 })[0].schemaPath, "/exclusiveMinimum");
  assert.equal(validate(5, { exclusiveMaximum: 5 })[0].schemaPath, "/exclusiveMaximum");
  assert.equal(validate(5, { const: 6 })[0].schemaPath, "/const");
});

test("keywords only apply to their own type (JSON Schema semantics)", () => {
  assert.deepEqual(validate("hello", { minimum: 10 }), []);
  assert.deepEqual(validate(3, { minLength: 5 }), []);
  assert.deepEqual(validate("x", { minItems: 2 }), []);
});
