import { test } from "node:test";
import assert from "node:assert/strict";
import { validate } from "../src/validator.ts";
import { assertSupportedSchema } from "../src/schema.ts";

test("valid document produces no errors", () => {
  const schema = {
    type: "object",
    properties: {
      name: { type: "string", minLength: 1 },
      age: { type: "integer", minimum: 0 },
      tags: { type: "array", items: { type: "string" } },
    },
    required: ["name"],
  } as const;
  assert.deepEqual(
    validate(schema, { name: "ada", age: 36, tags: ["math"] }),
    [],
  );
});

test("errors carry both instancePath and schemaPath", () => {
  const schema = {
    type: "object",
    properties: {
      user: {
        type: "object",
        properties: { age: { type: "integer", minimum: 0 } },
        required: ["age"],
      },
    },
    required: ["user"],
  } as const;
  const errors = validate(schema, { user: { age: -3 } });
  assert.equal(errors.length, 1);
  assert.equal(errors[0].instancePath, "/user/age");
  assert.equal(
    errors[0].schemaPath,
    "#/properties/user/properties/age/minimum",
  );
  assert.equal(errors[0].keyword, "minimum");
});

test("missing required property points at the parent instance", () => {
  const errors = validate(
    { type: "object", required: ["id"] },
    {},
  );
  assert.equal(errors.length, 1);
  assert.equal(errors[0].instancePath, "");
  assert.equal(errors[0].schemaPath, "#/required");
  assert.match(errors[0].message, /"id"/);
});

test("type mismatch short-circuits deeper keyword checks", () => {
  const errors = validate(
    { type: "string", minLength: 100 },
    42,
  );
  assert.equal(errors.length, 1);
  assert.equal(errors[0].keyword, "type");
});

test("array item errors are indexed", () => {
  const errors = validate(
    { type: "array", items: { type: "integer" } },
    [1, "two", 3],
  );
  assert.equal(errors.length, 1);
  assert.equal(errors[0].instancePath, "/1");
  assert.equal(errors[0].schemaPath, "#/items/type");
});

test("additionalProperties: false flags unknown keys", () => {
  const errors = validate(
    {
      type: "object",
      properties: { a: { type: "number" } },
      additionalProperties: false,
    },
    { a: 1, b: 2 },
  );
  assert.equal(errors.length, 1);
  assert.equal(errors[0].keyword, "additionalProperties");
  assert.match(errors[0].message, /"b"/);
});

test("enum, pattern, string and numeric bounds", () => {
  assert.equal(
    validate({ enum: ["x", "y"] }, "z").length,
    1,
  );
  assert.equal(
    validate({ type: "string", pattern: "^a+$" }, "ab").length,
    1,
  );
  assert.equal(
    validate({ type: "string", maxLength: 2 }, "abc").length,
    1,
  );
  assert.equal(
    validate({ type: "number", exclusiveMinimum: 5 }, 5).length,
    1,
  );
  assert.equal(
    validate({ type: "integer" }, 1.5).length,
    1,
  );
});

test("unsupported keywords are rejected at registration", () => {
  assert.throws(
    () => assertSupportedSchema({ $ref: "#/definitions/x" } as never),
    /unsupported schema keyword "\$ref"/,
  );
  assert.throws(
    () =>
      assertSupportedSchema({
        type: "object",
        properties: { a: { oneOf: [] } as never },
      }),
    /unsupported schema keyword "oneOf" at #\/properties\/a/,
  );
});
