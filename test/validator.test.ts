import { test } from "node:test";
import assert from "node:assert/strict";
import { validate, SUPPORTED_KEYWORDS } from "../src/validator.ts";

test("valid object passes with no errors", () => {
  const schema = {
    type: "object",
    required: ["name", "age"],
    properties: {
      name: { type: "string", minLength: 1, maxLength: 10 },
      age: { type: "integer", minimum: 0 },
    },
    additionalProperties: false,
  };
  const result = validate({ name: "amy", age: 3 }, schema);
  assert.equal(result.valid, true);
  assert.deepEqual(result.errors, []);
});

test("errors carry both instance path and rule path", () => {
  const schema = {
    type: "object",
    required: ["name"],
    properties: { age: { type: "integer", minimum: 0 } },
  };
  const result = validate({ age: -2 }, schema);
  assert.equal(result.valid, false);
  assert.equal(result.errors.length, 2);

  const requiredError = result.errors.find((e) => e.keyword === "required");
  assert.ok(requiredError);
  assert.equal(requiredError.instancePath, "");
  assert.equal(requiredError.rulePath, "/required");

  const minimumError = result.errors.find((e) => e.keyword === "minimum");
  assert.ok(minimumError);
  assert.equal(minimumError.instancePath, "/age");
  assert.equal(minimumError.rulePath, "/properties/age/minimum");
});

test("paths escape array indices and nested properties", () => {
  const schema = {
    type: "object",
    properties: {
      users: { type: "array", items: { type: "object", properties: { id: { type: "string" } } } },
    },
  };
  const result = validate({ users: [{ id: 1 }] }, schema);
  const typeError = result.errors.find((e) => e.keyword === "type");
  assert.ok(typeError);
  assert.equal(typeError.instancePath, "/users/0/id");
  assert.equal(typeError.rulePath, "/properties/users/items/properties/id/type");
});

test("enum, const, pattern and minItems produce rule paths", () => {
  assert.equal(validate("x", { type: "string", pattern: "^z$" }).errors[0].rulePath, "/pattern");
  assert.equal(validate(1, { const: 2 }).errors[0].keyword, "const");
  assert.equal(validate("a", { enum: ["b", "c"] }).errors[0].keyword, "enum");
  assert.equal(validate([], { type: "array", minItems: 2 }).errors[0].rulePath, "/minItems");
});

test("unsupported keywords are ignored but warned, never silently enforced", () => {
  const result = validate("not-an-email", { type: "string", format: "email" });
  assert.equal(result.valid, true);
  assert.match(result.warnings.join("\n"), /unsupported keyword "format"/);
});

test("the implemented subset is explicitly declared", () => {
  for (const keyword of ["type", "properties", "required", "additionalProperties", "items", "minimum", "maximum", "minLength", "maxLength", "pattern", "enum", "const", "minItems", "maxItems"]) {
    assert.ok((SUPPORTED_KEYWORDS as readonly string[]).includes(keyword), keyword);
  }
});
