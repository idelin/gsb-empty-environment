import { test } from "node:test";
import assert from "node:assert/strict";
import { allCounterexamples, listRules } from "../src/counterexample.ts";
import { validate } from "../src/validator.ts";
import type { Schema } from "../src/schema.ts";

const userSchema: Schema = {
  type: "object",
  properties: {
    name: { type: "string", minLength: 1, maxLength: 20, pattern: "^[A-Z][a-z]+$" },
    age: { type: "integer", minimum: 0, maximum: 150 },
    tags: { type: "array", minItems: 1, maxItems: 3, items: { enum: ["red", "green", "blue"] } },
    active: { type: "boolean" },
  },
  required: ["name", "age"],
  additionalProperties: false,
};

test("every emitted counterexample violates exactly the targeted rule", () => {
  const results = allCounterexamples(userSchema, 7);
  assert.ok(results.length > 0);
  for (const r of results) {
    if (!r.ok) continue; // explained failures are allowed, fabrications are not
    const errors = validate(r.instance, userSchema);
    assert.equal(errors.length, 1, `rule ${r.target.schemaPath}: expected exactly one error`);
    assert.equal(errors[0].schemaPath, r.target.schemaPath);
    assert.deepEqual(errors[0], r.error);
  }
});

test("expected rules yield counterexamples", () => {
  const results = allCounterexamples(userSchema, 7);
  const byPath = new Map(results.map((r) => [r.target.schemaPath, r]));
  const expectedOk = [
    "/type",
    "/required",
    "/additionalProperties",
    "/properties/name/type",
    "/properties/name/maxLength",
    "/properties/name/pattern",
    "/properties/age/minimum",
    "/properties/age/maximum",
    "/properties/tags/minItems",
    "/properties/tags/maxItems",
    "/properties/tags/items/enum",
    "/properties/active/type",
  ];
  for (const path of expectedOk) {
    const r = byPath.get(path);
    assert.ok(r, `missing rule target ${path}`);
    assert.ok(r.ok, `rule ${path} should have a counterexample, got: ${r.ok ? "" : r.reason}`);
  }
});

test("counterexample for /properties/age/minimum keeps maximum satisfied", () => {
  const results = allCounterexamples(userSchema, 7);
  const r = results.find((x) => x.target.schemaPath === "/properties/age/minimum");
  assert.ok(r && r.ok);
  if (r.ok) {
    const age = (r.instance as Record<string, unknown>).age as number;
    assert.ok(age < 0);
    assert.ok(age <= 150);
  }
});

test("un-isolatable violation is explained, not faked", () => {
  const pinned: Schema = { const: 5, minimum: 5, maximum: 5 };
  const results = allCounterexamples(pinned, 3);
  const byKeyword = new Map(results.map((r) => [r.target.keyword, r]));
  for (const kw of ["const", "minimum", "maximum"]) {
    const r = byKeyword.get(kw);
    assert.ok(r, `missing target /${kw}`);
    assert.ok(!r.ok, `/${kw} should not be isolatable`);
    if (!r.ok) assert.ok(r.reason.length > 0);
  }
});

test("minLength cannot be violated alone when the pattern forces length >= 2", () => {
  const r = allCounterexamples(userSchema, 7).find(
    (x) => x.target.schemaPath === "/properties/name/minLength",
  );
  assert.ok(r && !r.ok, "linked constraints make this violation un-isolatable");
  if (r && !r.ok) assert.ok(r.reason.length > 0);
});

test("minItems 0 cannot be violated", () => {
  const schema: Schema = { type: "array", minItems: 0, items: { type: "string" } };
  const r = allCounterexamples(schema, 1).find((x) => x.target.keyword === "minItems");
  assert.ok(r && !r.ok);
  if (r && !r.ok) assert.match(r.reason, /minItems is 0/);
});

test("contradictory schema has no positive example and no counterexamples", () => {
  const schema: Schema = { type: "integer", minimum: 10, maximum: 5 };
  const results = allCounterexamples(schema, 1);
  assert.ok(results.length > 0);
  for (const r of results) {
    assert.ok(!r.ok);
    if (!r.ok) assert.match(r.reason, /unsatisfiable/);
  }
});

test("listRules enumerates nested rules with paths", () => {
  const paths = listRules(userSchema).map((t) => t.schemaPath);
  assert.ok(paths.includes("/properties/age/minimum"));
  assert.ok(paths.includes("/properties/tags/items/enum"));
  assert.ok(paths.includes("/required"));
});
