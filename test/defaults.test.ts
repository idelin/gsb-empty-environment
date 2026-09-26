import { test } from "node:test";
import assert from "node:assert/strict";
import { validateAndFill } from "../src/index.ts";
import type { JsonObject, Schema } from "../src/index.ts";

test("fills defaults only for missing fields", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      a: { type: "string", default: "x" },
      b: { type: "number" }, // no default: stays absent
    },
  };
  const result = validateAndFill(schema, {});
  assert.ok(result.ok);
  const data = result.data as JsonObject;
  assert.equal(data.a, "x");
  assert.equal("b" in data, false);
});

test("never overwrites a present value", () => {
  const schema: Schema = {
    type: "object",
    properties: { a: { type: "string", default: "x" } },
  };
  const result = validateAndFill(schema, { a: "keep" });
  assert.ok(result.ok);
  assert.equal((result.data as JsonObject).a, "keep");
});

test("explicit null is not missing: no default is applied", () => {
  const schema: Schema = {
    type: "object",
    properties: { a: { type: ["string", "null"], default: "x" } },
  };
  const result = validateAndFill(schema, { a: null });
  assert.ok(result.ok);
  assert.strictEqual((result.data as JsonObject).a, null);
});

test("explicit null still fails a non-nullable type instead of being defaulted", () => {
  const schema: Schema = {
    type: "object",
    properties: { a: { type: "string", default: "x" } },
  };
  const result = validateAndFill(schema, { a: null });
  assert.ok(!result.ok);
  assert.deepEqual(result.errors[0], {
    instancePath: "/a",
    schemaPath: "#/properties/a/type",
    message: "expected type string, got null",
  });
});

test("dependent defaults are evaluated in topological order, not declaration order", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      c: { type: "string", default: { $from: "/b" } },
      b: { type: "string", default: { $from: "/a" } },
      a: { type: "string", default: "A" },
    },
  };
  const result = validateAndFill(schema, {});
  assert.ok(result.ok);
  assert.deepEqual(result.data, { a: "A", b: "A", c: "A" });
});

test("$from reads a user-supplied value when the dependency is present", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      name: { type: "string" },
      label: { type: "string", default: { $from: "/name" } },
    },
  };
  const result = validateAndFill(schema, { name: "delin" });
  assert.ok(result.ok);
  assert.equal((result.data as JsonObject).label, "delin");
});

test("sibling-relative $from works per array element", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      list: {
        type: "array",
        items: {
          type: "object",
          properties: {
            name: { type: "string" },
            label: { type: "string", default: { $from: "./name" } },
          },
        },
      },
    },
  };
  const result = validateAndFill(schema, { list: [{ name: "n1" }, { name: "n2" }] });
  assert.ok(result.ok);
  assert.deepEqual(result.data, { list: [{ name: "n1", label: "n1" }, { name: "n2", label: "n2" }] });
});

test("circular default dependencies report the full chain", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      a: { type: "string", default: { $from: "/b" } },
      b: { type: "string", default: { $from: "/a" } },
    },
  };
  const result = validateAndFill(schema, {});
  assert.ok(!result.ok);
  const messages = result.errors.map((e) => e.message).join("\n");
  assert.match(messages, /circular default dependency/);
  assert.ok(
    messages.includes("/a -> /b -> /a") || messages.includes("/b -> /a -> /b"),
    `expected the cycle chain in: ${messages}`,
  );
});

test("a self-referencing default is reported as a cycle", () => {
  const schema: Schema = {
    type: "object",
    properties: { a: { type: "string", default: { $from: "./a" } } },
  };
  const result = validateAndFill(schema, {});
  assert.ok(!result.ok);
  assert.match(result.errors[0].message, /\/a -> \/a/);
});

test("unresolvable dependency yields an error with instance and schema paths", () => {
  const schema: Schema = {
    type: "object",
    properties: { a: { type: "string", default: { $from: "/nope" } } },
  };
  const result = validateAndFill(schema, {});
  assert.ok(!result.ok);
  const error = result.errors[0];
  assert.equal(error.instancePath, "/a");
  assert.equal(error.schemaPath, "#/properties/a/default");
  assert.match(error.message, /\/nope/);
});

test("validation failure rolls back everything and never mutates the input", () => {
  const schema: Schema = {
    type: "object",
    required: ["req"],
    properties: {
      req: { type: "string" },
      other: { type: "string", default: "d" },
    },
  };
  const input = { req: 42 as unknown as string };
  const snapshot = structuredClone(input);
  const result = validateAndFill(schema, input);
  assert.ok(!result.ok);
  assert.equal("data" in result, false, "no partial data on failure");
  assert.deepEqual(input, snapshot, "input object must be untouched");
});

test("a default can satisfy a required property", () => {
  const schema: Schema = {
    type: "object",
    required: ["id"],
    properties: { id: { type: "string", default: "anonymous" } },
  };
  const result = validateAndFill(schema, {});
  assert.ok(result.ok);
  assert.equal((result.data as JsonObject).id, "anonymous");
});

test("array element default objects are independent (no shared mutable references)", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      list: {
        type: "array",
        items: {
          type: "object",
          properties: {
            meta: { type: "object", default: {} },
            tags: { type: "array", default: [] },
          },
        },
      },
    },
  };
  const result = validateAndFill(schema, { list: [{}, {}] });
  assert.ok(result.ok);
  const list = (result.data as JsonObject).list as JsonObject[];
  const meta0 = list[0].meta as JsonObject;
  const meta1 = list[1].meta as JsonObject;
  const tags0 = list[0].tags as unknown[];
  const tags1 = list[1].tags as unknown[];
  assert.notEqual(meta0, meta1);
  assert.notEqual(tags0, tags1);
  meta0.injected = true;
  tags0.push("x");
  assert.deepEqual(meta1, {});
  assert.deepEqual(tags1, []);
});

test("inserted defaults are clones: mutating the result does not corrupt the schema", () => {
  const schema: Schema = {
    type: "object",
    properties: { meta: { type: "object", default: { count: 0 } } },
  };
  const first = validateAndFill(schema, {});
  assert.ok(first.ok);
  ((first.data as JsonObject).meta as JsonObject).count = 999;
  const second = validateAndFill(schema, {});
  assert.ok(second.ok);
  assert.deepEqual((second.data as JsonObject).meta, { count: 0 });
  assert.deepEqual(schema.properties!.meta.default, { count: 0 });
});

test("defaults are filled inside freshly inserted default subtrees", () => {
  const schema: Schema = {
    type: "object",
    properties: {
      outer: {
        type: "object",
        default: {},
        properties: { inner: { type: "number", default: 7 } },
      },
    },
  };
  const result = validateAndFill(schema, {});
  assert.ok(result.ok);
  assert.deepEqual(result.data, { outer: { inner: 7 } });
});

test("an explicit null default is applied to a missing field", () => {
  const schema: Schema = {
    type: "object",
    properties: { a: { type: ["null", "string"], default: null } },
  };
  const result = validateAndFill(schema, {});
  assert.ok(result.ok);
  const data = result.data as JsonObject;
  assert.equal("a" in data, true);
  assert.strictEqual(data.a, null);
});
