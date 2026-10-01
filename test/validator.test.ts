import { test } from "node:test";
import assert from "node:assert/strict";
import { validate, type Schema } from "../src/validator.ts";
import { pathToString } from "../src/errors.ts";

const schema: Schema = {
  type: "object",
  properties: {
    name: { type: "string", minLength: 1 },
    qty: { type: "integer", minimum: 1 },
    tags: { type: "array", items: { type: "string" }, minItems: 1 },
  },
  required: ["name", "qty"],
  additionalProperties: false,
};

test("valid data produces no errors", () => {
  assert.deepEqual(
    validate(schema, { name: "a", qty: 2, tags: ["x"] }),
    [],
  );
});

test("errors carry both instance path and schema (rule) path", () => {
  const errors = validate(schema, { name: "ok", qty: 0 });
  assert.equal(errors.length, 1);
  assert.equal(errors[0].rule, "minimum");
  assert.equal(pathToString(errors[0].instancePath), "/qty");
  assert.equal(errors[0].schemaPath, "#/properties/qty/minimum");
});

test("required error points at the parent object", () => {
  const errors = validate(schema, { name: "ok" });
  assert.equal(errors.length, 1);
  assert.equal(errors[0].rule, "required");
  assert.equal(pathToString(errors[0].instancePath), "");
  assert.deepEqual(errors[0].params, { missingProperty: "qty" });
});

test("array item errors carry the numeric index in the instance path", () => {
  const errors = validate(schema, { name: "ok", qty: 1, tags: ["a", 7, "b", 9] });
  assert.equal(errors.length, 2);
  assert.deepEqual(errors[0].instancePath, ["tags", 1]);
  assert.deepEqual(errors[1].instancePath, ["tags", 3]);
  assert.equal(errors[0].schemaPath, "#/properties/tags/items/type");
});

test("additionalProperties reports each extra key", () => {
  const errors = validate(schema, { name: "ok", qty: 1, extra: 1, more: 2 });
  assert.equal(errors.length, 2);
  assert.ok(errors.every((e) => e.rule === "additionalProperties"));
  assert.deepEqual(errors[0].instancePath, ["extra"]);
});

test("type mismatch stops descent into deeper keywords", () => {
  const errors = validate(schema, "not-an-object");
  assert.equal(errors.length, 1);
  assert.equal(errors[0].rule, "type");
});

test("enum and const are enforced", () => {
  const s: Schema = { type: "string", enum: ["a", "b"] };
  assert.equal(validate(s, "a").length, 0);
  assert.equal(validate(s, "z")[0].rule, "enum");
  assert.equal(validate({ const: 42 }, 41)[0].rule, "const");
});

test("schema version option tags every error", () => {
  const errors = validate(schema, { qty: 0 }, { schemaVersion: "v2" });
  assert.ok(errors.length >= 2);
  assert.ok(errors.every((e) => e.schemaVersion === "v2"));
});
