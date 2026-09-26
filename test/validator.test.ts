import assert from "node:assert/strict";
import { test } from "node:test";
import { applyDefaultsAndValidate, type Schema } from "../src/index.ts";

test("fills literal defaults only for missing fields", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      host: { type: "string", default: "localhost" },
      port: { type: "integer", default: 8080 },
    },
  };
  const res = applyDefaultsAndValidate(schema, { port: 1234 });
  assert.equal(res.ok, true);
  if (res.ok) {
    assert.deepEqual(res.value, { host: "localhost", port: 1234 });
  }
});

test("explicit null is not missing: default is not applied", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      note: { default: "fallback" }, // no type constraint: null is allowed
    },
  };
  const res = applyDefaultsAndValidate(schema, { note: null });
  assert.equal(res.ok, true);
  if (res.ok) assert.deepEqual(res.value, { note: null });
});

test("explicit null still fails an incompatible type check", () => {
  const schema: Schema = {
    type: "object",
    properties: { name: { type: "string", default: "anon" } },
  };
  const res = applyDefaultsAndValidate(schema, { name: null });
  assert.equal(res.ok, false);
  if (!res.ok) {
    assert.equal(res.errors[0].keyword, "type");
    assert.equal(res.errors[0].instancePath, "/name");
  }
});

test("expression defaults are evaluated in topological order", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      // declared "bottom-up" on purpose: c depends on b, b depends on a
      c: { type: "string", default: { $expr: { op: "concat", args: [{ $get: "b" }, "!",] } } },
      b: { type: "string", default: { $expr: { op: "concat", args: [{ $get: "a" }, "-b"] } } },
      a: { type: "string", default: "a" },
    },
  };
  const res = applyDefaultsAndValidate(schema, {});
  assert.equal(res.ok, true);
  if (res.ok) assert.deepEqual(res.value, { a: "a", b: "a-b", c: "a-b!" });
});

test("expression default can compute from provided sibling fields", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      firstName: { type: "string" },
      lastName: { type: "string" },
      displayName: {
        type: "string",
        default: { $expr: { op: "concat", args: [{ $get: "firstName" }, " ", { $get: "lastName" }] } },
      },
      total: {
        type: "number",
        default: { $expr: { op: "mul", args: [{ $get: "qty" }, { $get: "price" }] } },
      },
      qty: { type: "integer", default: 2 },
      price: { type: "number", default: 9.5 },
    },
    required: ["firstName", "lastName"],
  };
  const res = applyDefaultsAndValidate(schema, { firstName: "Ada", lastName: "Lovelace" });
  assert.equal(res.ok, true);
  if (res.ok) {
    assert.deepEqual(res.value, {
      firstName: "Ada",
      lastName: "Lovelace",
      displayName: "Ada Lovelace",
      qty: 2,
      price: 9.5,
      total: 19,
    });
  }
});

test("dependency cycle is reported with the full chain", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      a: { default: { $expr: { op: "concat", args: [{ $get: "c" }, "a"] } } },
      b: { default: { $expr: { op: "concat", args: [{ $get: "a" }, "b"] } } },
      c: { default: { $expr: { op: "concat", args: [{ $get: "b" }, "c"] } } },
    },
  };
  const input = {};
  const res = applyDefaultsAndValidate(schema, input);
  assert.equal(res.ok, false);
  if (!res.ok) {
    const cycle = res.errors.find((e) => e.keyword === "defaultCycle");
    assert.ok(cycle, "expected a defaultCycle error");
    assert.match(cycle.message, /default dependency cycle: [abc] -> [abc] -> [abc] -> [abc]/);
    // chain starts and ends with the same field
    const chain = cycle.message.replace("default dependency cycle: ", "").split(" -> ");
    assert.equal(chain[0], chain[chain.length - 1]);
    assert.equal(chain.length, 4);
  }
  assert.deepEqual(input, {}); // rolled back
});

test("validation failure rolls back all filled defaults", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      kind: { type: "string", default: "user" },
      age: { type: "integer", minimum: 0 },
    },
    required: ["age"],
  };
  const input = { age: -5 };
  const snapshot = JSON.parse(JSON.stringify(input));
  const res = applyDefaultsAndValidate(schema, input);
  assert.equal(res.ok, false);
  if (!res.ok) {
    assert.equal(res.errors.length, 1);
    assert.equal(res.errors[0].keyword, "minimum");
    assert.equal(res.errors[0].instancePath, "/age");
    assert.equal(res.errors[0].schemaPath, "#/properties/age/minimum");
  }
  assert.deepEqual(input, snapshot); // "kind" default never leaks into the caller's object
});

test("nested array elements get independent copies of object defaults", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      rows: {
        type: "array",
        items: {
          type: "object",
          properties: {
            meta: { type: "object", default: { tags: [], flags: { seen: false } } },
            name: { type: "string", default: "row" },
          },
        },
      },
    },
  };
  const res = applyDefaultsAndValidate(schema, { rows: [{}, {}, {}] });
  assert.equal(res.ok, true);
  if (!res.ok) return;
  const rows = (res.value as { rows: { meta: { tags: string[]; flags: { seen: boolean } }; name: string }[] }).rows;
  assert.equal(rows.length, 3);
  // mutate element 0 deeply; others must be unaffected
  rows[0].meta.tags.push("x");
  rows[0].meta.flags.seen = true;
  assert.deepEqual(rows[1].meta, { tags: [], flags: { seen: false } });
  assert.deepEqual(rows[2].meta, { tags: [], flags: { seen: false } });
  // the schema's own default literal must not be polluted either
  const itemsSchema = schema.properties!.rows.items!;
  assert.deepEqual(itemsSchema.properties!.meta.default, { tags: [], flags: { seen: false } });
});

test("defaults filled inside array elements validate per element", () => {
  const schema: Schema = {
    type: "array",
    items: {
      type: "object",
      properties: {
        id: { type: "integer" },
        label: { type: "string", default: "item" },
      },
      required: ["id"],
    },
  };
  const res = applyDefaultsAndValidate(schema, [{ id: 1 }, { id: "oops" }]);
  assert.equal(res.ok, false);
  if (!res.ok) {
    assert.equal(res.errors[0].instancePath, "/1/id");
    assert.equal(res.errors[0].schemaPath, "#/items/properties/id/type");
  }
});

test("errors carry both instance path and schema path", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      address: {
        type: "object",
        properties: { zip: { type: "string", pattern: "^[0-9]{5}$" } },
        required: ["zip"],
      },
    },
  };
  const res = applyDefaultsAndValidate(schema, { address: { zip: "ABC" } });
  assert.equal(res.ok, false);
  if (!res.ok) {
    assert.deepEqual(res.errors[0], {
      instancePath: "/address/zip",
      schemaPath: "#/properties/address/properties/zip/pattern",
      keyword: "pattern",
      message: "string does not match pattern /^[0-9]{5}$/",
    });
  }
});

test("default can satisfy required", () => {
  const schema: Schema = {
    type: "object",
    properties: { status: { type: "string", default: "draft", enum: ["draft", "published"] } },
    required: ["status"],
  };
  const res = applyDefaultsAndValidate(schema, {});
  assert.equal(res.ok, true);
  if (res.ok) assert.deepEqual(res.value, { status: "draft" });
});

test("unresolvable $get reference is an error with both paths", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      a: { default: { $expr: { op: "concat", args: [{ $get: "ghost" }] } } },
    },
  };
  const res = applyDefaultsAndValidate(schema, {});
  assert.equal(res.ok, false);
  if (!res.ok) {
    assert.equal(res.errors[0].keyword, "default");
    assert.equal(res.errors[0].instancePath, "/a");
    assert.equal(res.errors[0].schemaPath, "#/properties/a/default");
    assert.match(res.errors[0].message, /cannot resolve reference "ghost"/);
  }
});

test("validator subset: enum, const, additionalProperties, min/max, minItems", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      color: { type: "string", enum: ["red", "green"] },
      version: { const: 1 },
      score: { type: "number", exclusiveMinimum: 0, maximum: 100 },
      tags: { type: "array", minItems: 2, items: { type: "string" } },
    },
    additionalProperties: false,
  };
  const res = applyDefaultsAndValidate(schema, {
    color: "blue",
    version: 2,
    score: 0,
    tags: ["only"],
    extra: true,
  });
  assert.equal(res.ok, false);
  if (!res.ok) {
    const byKeyword = new Map(res.errors.map((e) => [e.keyword, e]));
    assert.equal(byKeyword.get("enum")?.instancePath, "/color");
    assert.equal(byKeyword.get("const")?.instancePath, "/version");
    assert.equal(byKeyword.get("exclusiveMinimum")?.instancePath, "/score");
    assert.equal(byKeyword.get("minItems")?.instancePath, "/tags");
    assert.equal(byKeyword.get("additionalProperties")?.instancePath, "");
    assert.equal(byKeyword.get("additionalProperties")?.schemaPath, "#/additionalProperties");
  }
});

test("integer type accepts integers and number accepts both", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      i: { type: "integer" },
      n: { type: "number" },
    },
  };
  assert.equal(applyDefaultsAndValidate(schema, { i: 3, n: 3 }).ok, true);
  assert.equal(applyDefaultsAndValidate(schema, { i: 3, n: 3.5 }).ok, true);
  assert.equal(applyDefaultsAndValidate(schema, { i: 3.5, n: 3 }).ok, false);
});

test("input instance is never mutated, on success or failure", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      a: { type: "string", default: "x" },
      nested: {
        type: "object",
        properties: { b: { type: "string", default: "y" } },
      },
    },
  };
  const good = { nested: {} };
  const bad = { a: null, nested: {} };
  applyDefaultsAndValidate(schema, good);
  applyDefaultsAndValidate(schema, bad);
  assert.deepEqual(good, { nested: {} });
  assert.deepEqual(bad, { a: null, nested: {} });
});

test("default literal object gets further nested defaults applied", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      config: {
        type: "object",
        default: {},
        properties: {
          retries: { type: "integer", default: 3 },
        },
      },
    },
  };
  const res = applyDefaultsAndValidate(schema, {});
  assert.equal(res.ok, true);
  if (res.ok) assert.deepEqual(res.value, { config: { retries: 3 } });
});
