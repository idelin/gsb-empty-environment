import { test } from "node:test";
import assert from "node:assert/strict";
import { validate } from "../src/validate.ts";
import type { Schema } from "../src/types.ts";

const schema: Schema = {
  type: "object",
  required: ["user"],
  properties: {
    user: {
      type: "object",
      required: ["age"],
      properties: {
        age: { type: "integer", minimum: 0, maximum: 150 },
        name: { type: "string", minLength: 1, maxLength: 5 },
        tags: { type: "array", minItems: 1, items: { type: "string" } },
      },
    },
  },
};

test("accepts a conforming instance", () => {
  assert.deepEqual(validate({ user: { age: 3, name: "Ada", tags: ["x"] } }, schema), []);
});

test("type error carries instance path and rule path", () => {
  const [err] = validate({ user: { age: 1.5 } }, schema);
  assert.equal(err.instancePath, "$.user.age");
  assert.equal(err.rulePath, "#/properties/user/properties/age/type");
  assert.match(err.message, /integer/);
});

test("range error points at the violated rule", () => {
  const [err] = validate({ user: { age: 200 } }, schema);
  assert.equal(err.instancePath, "$.user.age");
  assert.equal(err.rulePath, "#/properties/user/properties/age/maximum");
});

test("missing required property is reported on the containing object", () => {
  const [err] = validate({ user: {} }, schema);
  assert.equal(err.instancePath, "$.user");
  assert.equal(err.rulePath, "#/properties/user/required");
  assert.match(err.message, /"age"/);
});

test("array item errors are located by index", () => {
  const [err] = validate({ user: { age: 1, tags: ["ok", 42] } }, schema);
  assert.equal(err.instancePath, "$.user.tags[1]");
  assert.equal(err.rulePath, "#/properties/user/properties/tags/items/type");
});

test("enum violation is reported", () => {
  const s: Schema = { type: "string", enum: ["a", "b"] };
  const [err] = validate("c", s);
  assert.equal(err.instancePath, "$");
  assert.equal(err.rulePath, "#/enum");
});

test("unobserved properties are NOT forbidden", () => {
  assert.deepEqual(validate({ user: { age: 1 }, brandNewField: { anything: true } }, schema), []);
});

test("nullable via union type", () => {
  const s: Schema = { type: ["string", "null"] };
  assert.deepEqual(validate(null, s), []);
  assert.equal(validate(1, s).length, 1);
});

test("integer type rejects decimals but number accepts them", () => {
  assert.equal(validate(1.5, { type: "integer" }).length, 1);
  assert.deepEqual(validate(1.5, { type: "number" }), []);
  assert.deepEqual(validate(2, { type: "number" }), []);
});
