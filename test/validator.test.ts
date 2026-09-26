import { test } from "node:test";
import assert from "node:assert/strict";
import { SchemaError, validateAndFill } from "../src/index.ts";
import type { Schema } from "../src/index.ts";

test("type errors carry both instance path and schema path", () => {
  const schema: Schema = {
    type: "object",
    properties: { age: { type: "integer" } },
  };
  const result = validateAndFill(schema, { age: "old" });
  assert.ok(!result.ok);
  assert.deepEqual(result.errors[0], {
    instancePath: "/age",
    schemaPath: "#/properties/age/type",
    message: "expected type integer, got string",
  });
});

test("nested array element errors point at the exact element and rule", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      list: {
        type: "array",
        items: {
          type: "object",
          properties: { x: { type: "string" } },
        },
      },
    },
  };
  const result = validateAndFill(schema, { list: [{ x: "ok" }, { x: 1 }] });
  assert.ok(!result.ok);
  assert.equal(result.errors.length, 1);
  assert.equal(result.errors[0].instancePath, "/list/1/x");
  assert.equal(result.errors[0].schemaPath, "#/properties/list/items/properties/x/type");
});

test("required violations are reported at the owning object", () => {
  const schema: Schema = { type: "object", required: ["id"], properties: { id: { type: "string" } } };
  const result = validateAndFill(schema, {});
  assert.ok(!result.ok);
  assert.deepEqual(result.errors[0], {
    instancePath: "",
    schemaPath: "#/required",
    message: 'missing required property "id"',
  });
});

test("additionalProperties: false rejects unknown keys", () => {
  const schema: Schema = {
    type: "object",
    additionalProperties: false,
    properties: { a: { type: "string" } },
  };
  const result = validateAndFill(schema, { a: "ok", extra: 1 });
  assert.ok(!result.ok);
  assert.equal(result.errors[0].instancePath, "/extra");
  assert.equal(result.errors[0].schemaPath, "#/additionalProperties");
});

test("scalar keywords: enum, const, pattern, length, numeric ranges", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      color: { type: "string", enum: ["red", "green"] },
      kind: { const: "fixed" },
      code: { type: "string", pattern: "^[A-Z]{2}$" },
      name: { type: "string", minLength: 2, maxLength: 3 },
      score: { type: "number", minimum: 0, maximum: 10 },
      ratio: { type: "number", exclusiveMinimum: 0, exclusiveMaximum: 1 },
    },
  };
  const bad = validateAndFill(schema, {
    color: "blue",
    kind: "other",
    code: "ab",
    name: "toolong",
    score: 11,
    ratio: 1,
  });
  assert.ok(!bad.ok);
  const paths = bad.errors.map((e) => `${e.instancePath} ${e.schemaPath}`);
  assert.deepEqual(paths, [
    "/color #/properties/color/enum",
    "/kind #/properties/kind/const",
    "/code #/properties/code/pattern",
    "/name #/properties/name/maxLength",
    "/score #/properties/score/maximum",
    "/ratio #/properties/ratio/exclusiveMaximum",
  ]);
  const good = validateAndFill(schema, {
    color: "red",
    kind: "fixed",
    code: "AB",
    name: "ab",
    score: 5,
    ratio: 0.5,
  });
  assert.ok(good.ok);
});

test("array size keywords: minItems and maxItems", () => {
  const schema: Schema = { type: "array", minItems: 2, maxItems: 3, items: { type: "number" } };
  assert.ok(!validateAndFill(schema, [1]).ok);
  assert.ok(validateAndFill(schema, [1, 2, 3]).ok);
  assert.ok(!validateAndFill(schema, [1, 2, 3, 4]).ok);
});

test("integer accepts whole numbers and rejects fractions; number accepts both", () => {
  const asInteger: Schema = { type: "integer" };
  assert.ok(validateAndFill(asInteger, 3).ok);
  assert.ok(!validateAndFill(asInteger, 1.5).ok);
  const asNumber: Schema = { type: "number" };
  assert.ok(validateAndFill(asNumber, 1.5).ok);
});

test("null only matches the null type (or a union containing it)", () => {
  assert.ok(validateAndFill({ type: "null" }, null).ok);
  assert.ok(validateAndFill({ type: ["string", "null"] }, null).ok);
  assert.ok(!validateAndFill({ type: "string" }, null).ok);
  assert.ok(!validateAndFill({ type: "object" }, null).ok);
});

test("union types accept any listed type", () => {
  const schema: Schema = { type: ["string", "number"] };
  assert.ok(validateAndFill(schema, "s").ok);
  assert.ok(validateAndFill(schema, 1).ok);
  assert.ok(!validateAndFill(schema, true).ok);
});

test("an invalid pattern is a schema authoring error, not a validation error", () => {
  const schema: Schema = { type: "string", pattern: "(" };
  assert.throws(() => validateAndFill(schema, "x"), SchemaError);
});
