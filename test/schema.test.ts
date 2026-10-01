import { test } from "node:test";
import assert from "node:assert/strict";
import { SchemaRegistry, walkData, type TypeNode, type RefOccurrence } from "../src/schema.ts";
import type { ValidationError } from "../src/errors.ts";

function makeWalk() {
  return { errors: [] as ValidationError[], refs: [] as RefOccurrence[] };
}

test("registration rejects keywords outside the promised subset", () => {
  const registry = new SchemaRegistry();
  assert.throws(
    () =>
      registry.register({
        name: "bad",
        version: 1,
        root: { kind: "object", properties: {}, additionalProperties: false } as unknown as TypeNode,
      }),
    /unsupported-schema.*additionalProperties/,
  );
  assert.throws(
    () =>
      registry.register({
        name: "bad2",
        version: 1,
        root: { kind: "string", pattern: "^a" } as unknown as TypeNode,
      }),
    /unsupported-schema.*pattern/,
  );
  assert.throws(
    () => registry.register({ name: "bad3", version: 1, root: { kind: "oneOf" } as unknown as TypeNode }),
    /unsupported-schema/,
  );
});

test("duplicate schema name@version is rejected, versions coexist", () => {
  const registry = new SchemaRegistry();
  registry.register({ name: "t", version: 1, root: { kind: "string" } });
  registry.register({ name: "t", version: 2, root: { kind: "integer" } });
  assert.throws(() => registry.register({ name: "t", version: 1, root: { kind: "string" } }), /already registered/);
  assert.equal(registry.get("t")!.version, 2, "latest version wins when unpinned");
  assert.equal(registry.get("t", 1)!.root.kind, "string");
});

test("data validation reports instance path and rule path", () => {
  const root: TypeNode = {
    kind: "object",
    required: ["name"],
    properties: {
      name: { kind: "string" },
      age: { kind: "integer" },
      tags: { kind: "array", items: { kind: "string" }, maxItems: 2 },
    },
  };
  const walk = makeWalk();
  walkData(root, { age: "old", tags: ["a", "b", "c"], extra: 1 }, "doc1", "t@1", "", "", walk);
  const codes = walk.errors.map((e) => e.code).sort();
  assert.deepEqual(codes, ["array-size", "missing-required", "type-mismatch", "unknown-property"]);

  const typeErr = walk.errors.find((e) => e.code === "type-mismatch")!;
  assert.equal(typeErr.instancePath, "/age");
  assert.equal(typeErr.rulePath, "/properties/age");
  assert.deepEqual(typeErr.docs, [{ id: "doc1", path: "/age", value: "old" }]);

  const sizeErr = walk.errors.find((e) => e.code === "array-size")!;
  assert.equal(sizeErr.instancePath, "/tags");
  assert.equal(sizeErr.rulePath, "/properties/tags");

  const reqErr = walk.errors.find((e) => e.code === "missing-required")!;
  assert.equal(reqErr.instancePath, "");
  assert.equal(reqErr.rulePath, "/required");
});

test("nested array item errors carry the full original path", () => {
  const root: TypeNode = {
    kind: "object",
    properties: { items: { kind: "array", items: { kind: "integer" } } },
  };
  const walk = makeWalk();
  walkData(root, { items: [1, "two", 3] }, "d", "t@1", "", "", walk);
  assert.equal(walk.errors.length, 1);
  assert.equal(walk.errors[0].instancePath, "/items/1");
  assert.equal(walk.errors[0].rulePath, "/properties/items/items");
});

test("enum validation stays inside the subset", () => {
  const walk = makeWalk();
  walkData({ kind: "enum", values: ["a", "b"] }, "c", "d", "t@1", "/v", "/properties/v", walk);
  assert.equal(walk.errors.length, 1);
  assert.equal(walk.errors[0].code, "enum-mismatch");
});
