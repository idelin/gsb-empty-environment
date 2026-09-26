import { describe, it } from "node:test";
import assert from "node:assert/strict";

import { compileValidator, validate, SchemaError, type Schema } from "../src/index.ts";

const userSchema: Schema = {
  type: "object",
  properties: {
    id: { type: "integer" },
    name: { type: "string" },
    active: { type: "boolean" },
    deletedAt: { type: "null" },
    address: {
      type: "object",
      properties: { zip: { type: "string" } },
      required: ["zip"],
    },
  },
  required: ["id", "name"],
  additionalProperties: false,
};

describe("schema subset validator", () => {
  it("accepts a fully valid record", () => {
    const errors = validate(userSchema, {
      id: 1,
      name: "ada",
      active: true,
      deletedAt: null,
      address: { zip: "10000" },
    });
    assert.deepEqual(errors, []);
  });

  it("reports instancePath and rulePath for a nested type mismatch", () => {
    const errors = validate(userSchema, { id: 1, name: "ada", address: { zip: 42 } });
    assert.equal(errors.length, 1);
    assert.equal(errors[0].keyword, "type");
    assert.equal(errors[0].instancePath, "/address/zip");
    assert.equal(errors[0].rulePath, "/properties/address/properties/zip/type");
  });

  it("reports missing required properties with the object as instancePath", () => {
    const errors = validate(userSchema, { name: "ada" });
    assert.equal(errors.length, 1);
    assert.equal(errors[0].keyword, "required");
    assert.equal(errors[0].instancePath, "");
    assert.equal(errors[0].rulePath, "/required");
    assert.match(errors[0].message, /"id"/);
  });

  it("reports additional properties when additionalProperties is false", () => {
    const errors = validate(userSchema, { id: 1, name: "ada", extra: 1 });
    assert.equal(errors.length, 1);
    assert.equal(errors[0].keyword, "additionalProperties");
    assert.equal(errors[0].instancePath, "");
    assert.equal(errors[0].rulePath, "/additionalProperties");
    assert.match(errors[0].message, /"extra"/);
  });

  it("distinguishes integer from number", () => {
    const schema: Schema = { type: "integer" };
    assert.deepEqual(validate(schema, 3), []);
    assert.equal(validate(schema, 3.5).length, 1);
    assert.equal(validate(schema, NaN).length, 1);
    assert.equal(validate(schema, Infinity).length, 1);
  });

  it("rejects non-finite numbers for type number", () => {
    const schema: Schema = { type: "number" };
    assert.deepEqual(validate(schema, 3.5), []);
    assert.equal(validate(schema, NaN).length, 1);
    assert.equal(validate(schema, -Infinity).length, 1);
  });

  it("escapes ~ and / in instance and rule paths", () => {
    const schema: Schema = {
      type: "object",
      properties: { "a/b~c": { type: "string" } },
    };
    const errors = validate(schema, { "a/b~c": 7 });
    assert.equal(errors.length, 1);
    assert.equal(errors[0].instancePath, "/a~1b~0c");
    assert.equal(errors[0].rulePath, "/properties/a~1b~0c/type");
  });

  it("rejects unsupported keywords instead of silently ignoring them", () => {
    assert.throws(
      () => compileValidator({ type: "string", minLength: 3 } as unknown as Schema),
      (err: unknown) => err instanceof SchemaError && /minLength/.test((err as Error).message),
    );
  });

  it("rejects unsupported types", () => {
    assert.throws(
      () => compileValidator({ type: "array" } as unknown as Schema),
      SchemaError,
    );
  });

  it("flags a non-object record against an object schema", () => {
    const errors = validate(userSchema, "nope");
    assert.equal(errors.length, 1);
    assert.equal(errors[0].instancePath, "");
    assert.equal(errors[0].rulePath, "/type");
  });
});
