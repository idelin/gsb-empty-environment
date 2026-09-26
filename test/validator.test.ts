import { test } from "node:test";
import assert from "node:assert/strict";
import { validate } from "../src/validator.ts";
import type { Schema } from "../src/schema.ts";

const userSchema: Schema = {
  type: "object",
  required: ["name", "age"],
  properties: {
    name: { type: "string", minLength: 1 },
    age: { type: "integer", minimum: 0 },
    email: { type: "string", pattern: "^[^@]+@[^@]+$" },
    tags: { type: "array", items: { type: "string" }, maxItems: 3 },
    role: { enum: ["admin", "user"] },
  },
  additionalProperties: false,
};

test("合法实例通过", () => {
  assert.deepEqual(
    validate({ name: "Ada", age: 36, email: "ada@example.com", tags: ["math"], role: "admin" }, userSchema),
    [],
  );
});

test("required 缺失：给出实例路径与规则路径", () => {
  const [err] = validate({ age: 1 }, userSchema);
  assert.equal(err.keyword, "required");
  assert.equal(err.instancePath, "");
  assert.equal(err.schemaPath, "/required");
  assert.match(err.message, /name/);
});

test("嵌套属性：instancePath 与 schemaPath 同步深入", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      address: {
        type: "object",
        properties: { zip: { type: "string", pattern: "^\\d{6}$" } },
      },
    },
  };
  const [err] = validate({ address: { zip: "abc" } }, schema);
  assert.equal(err.instancePath, "/address/zip");
  assert.equal(err.schemaPath, "/properties/address/properties/zip/pattern");
  assert.equal(err.keyword, "pattern");
});

test("数组元素错误定位到具体下标", () => {
  const [err] = validate({ name: "A", age: 1, tags: ["ok", 42] }, userSchema);
  assert.equal(err.instancePath, "/tags/1");
  assert.equal(err.schemaPath, "/properties/tags/items/type");
});

test("additionalProperties=false 报告多余字段", () => {
  const [err] = validate({ name: "A", age: 1, extra: true }, userSchema);
  assert.equal(err.keyword, "additionalProperties");
  assert.equal(err.schemaPath, "/additionalProperties");
  assert.match(err.message, /extra/);
});

test("数值边界与枚举", () => {
  assert.equal(validate({ name: "A", age: -1 }, userSchema)[0].keyword, "minimum");
  assert.equal(validate({ name: "A", age: 1, role: "root" }, userSchema)[0].keyword, "enum");
});

test("一次收集多个错误而非遇错即停", () => {
  const errors = validate({ age: "x", extra: 1 }, userSchema);
  assert.ok(errors.length >= 3); // required(name) + type(age) + additionalProperties(extra)
});

test("type 数组与 integer 判定", () => {
  const schema: Schema = { type: ["string", "integer"] };
  assert.deepEqual(validate("a", schema), []);
  assert.deepEqual(validate(2, schema), []);
  assert.equal(validate(2.5, schema)[0].keyword, "type"); // 2.5 不是 integer
  assert.equal(validate(2.5, { type: "integer" })[0].keyword, "type");
  assert.equal(validate(true, schema)[0].keyword, "type");
});
