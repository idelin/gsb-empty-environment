import { test } from "node:test";
import assert from "node:assert/strict";
import { Registry, SchemaConflictError } from "../src/registry.ts";
import { schemaDigest } from "../src/digest.ts";

const schemaV1 = {
  type: "object",
  properties: { name: { type: "string" } },
  required: ["name"],
} as const;

test("digest is stable regardless of key order", () => {
  const a = schemaDigest({ type: "object", required: ["x"] });
  const b = schemaDigest({ required: ["x"], type: "object" });
  assert.equal(a, b);
});

test("re-registering identical schema is a no-op", () => {
  const reg = new Registry();
  const d1 = reg.registerSchema("1.0", { ...schemaV1 });
  const d2 = reg.registerSchema("1.0", { ...schemaV1 });
  assert.equal(d1, d2);
  assert.deepEqual(reg.versions(), ["1.0"]);
});

test("same version with different digest is a conflict", () => {
  const reg = new Registry();
  reg.registerSchema("1.0", { ...schemaV1 });
  assert.throws(
    () =>
      reg.registerSchema("1.0", {
        type: "object",
        properties: { name: { type: "string", minLength: 1 } },
        required: ["name"],
      }),
    (err: unknown) => {
      assert.ok(err instanceof SchemaConflictError);
      assert.equal(err.version, "1.0");
      assert.notEqual(err.existingDigest, err.incomingDigest);
      return true;
    },
  );
});

test("migration steps require registered endpoints and are unique", () => {
  const reg = new Registry();
  reg.registerSchema("1.0", { ...schemaV1 });
  assert.throws(
    () => reg.registerMigration("1.0", "2.0", (d) => d),
    /unknown target version/,
  );
  reg.registerSchema("2.0", { ...schemaV1 });
  reg.registerMigration("1.0", "2.0", (d) => d);
  assert.throws(
    () => reg.registerMigration("1.0", "2.0", (d) => d),
    /already registered/,
  );
});

test("findMigrationPath follows only explicit steps", () => {
  const reg = new Registry();
  for (const v of ["1.0", "2.0", "3.0"]) reg.registerSchema(v, { ...schemaV1 });
  reg.registerMigration("1.0", "2.0", (d) => d);
  reg.registerMigration("2.0", "3.0", (d) => d);
  const path = reg.findMigrationPath("1.0", "3.0");
  assert.deepEqual(
    path?.map((m) => [m.from, m.to].join("->")),
    ["1.0->2.0", "2.0->3.0"],
  );
  assert.equal(reg.findMigrationPath("3.0", "1.0"), null);
  assert.equal(reg.findMigrationPath("1.0", "1.0")?.length, 0);
});
