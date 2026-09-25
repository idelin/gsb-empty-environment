import { test } from "node:test";
import assert from "node:assert/strict";
import { validate, type Schema } from "../src/schema.ts";

const userSchema: Schema = {
  type: "object",
  properties: {
    name: { type: "string" },
    age: { type: "number" },
    tags: { type: "array", items: { type: "string" }, minItems: 1 },
  },
  required: ["name", "tags"],
  additionalProperties: false,
};

test("valid document passes", () => {
  assert.deepEqual(
    validate({ name: "Ada", tags: ["admin"], age: 36 }, userSchema),
    [],
  );
});

test("missing required property is located at its path", () => {
  const issues = validate({ tags: ["a"] }, userSchema);
  assert.deepEqual(issues, [
    { path: "$.name", message: "missing required property" },
  ]);
});

test("wrong type, extra property, and array item errors carry paths", () => {
  const issues = validate(
    { name: 7, tags: ["ok", 5], extra: true },
    userSchema,
  );
  const paths = issues.map((i) => i.path).sort();
  assert.deepEqual(paths, ["$.extra", "$.name", "$.tags[1]"]);
});

test("minItems violation is reported on the array path", () => {
  const issues = validate({ name: "x", tags: [] }, userSchema);
  assert.deepEqual(issues, [
    { path: "$.tags", message: "expected at least 1 items, got 0" },
  ]);
});

test("nested object paths", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      address: {
        type: "object",
        properties: { zip: { type: "string" } },
        required: ["zip"],
      },
    },
  };
  const issues = validate({ address: { zip: 123 } }, schema);
  assert.deepEqual(issues, [
    { path: "$.address.zip", message: "expected string, got number" },
  ]);
});
