import { test } from "node:test";
import assert from "node:assert/strict";
import { Registry } from "../src/registry.ts";
import { processDocument } from "../src/processor.ts";

function buildRegistry(): Registry {
  const reg = new Registry();
  reg.registerSchema("1.0", {
    type: "object",
    properties: {
      version: { type: "string" },
      name: { type: "string", minLength: 1 },
    },
    required: ["version", "name"],
    additionalProperties: false,
  });
  reg.registerSchema("2.0", {
    type: "object",
    properties: {
      version: { type: "string" },
      fullName: { type: "string", minLength: 1 },
      email: { type: "string", pattern: "^[^@]+@[^@]+$" },
    },
    required: ["version", "fullName", "email"],
    additionalProperties: false,
  });
  reg.registerMigration("1.0", "2.0", (doc) => {
    const { name, ...rest } = doc;
    if (typeof name !== "string") throw new Error("name must be a string");
    return { ...rest, version: "2.0", fullName: name, email: "unknown@example.com" };
  });
  return reg;
}

test("current-version document validates without migration", () => {
  const reg = buildRegistry();
  const res = processDocument(reg, { version: "2.0", fullName: "Ada", email: "a@b.c" });
  assert.ok(res.ok);
  assert.equal(res.migrated, false);
  assert.deepEqual(res.migrationPath, ["2.0"]);
});

test("old document migrates through the explicit chain and revalidates", () => {
  const reg = buildRegistry();
  const res = processDocument(reg, { version: "1.0", name: "Ada" });
  assert.ok(res.ok);
  assert.equal(res.migrated, true);
  assert.deepEqual(res.migrationPath, ["1.0", "2.0"]);
  assert.deepEqual(res.document, {
    version: "2.0",
    fullName: "Ada",
    email: "unknown@example.com",
  });
});

test("missing version is rejected unless explicitly assumed", () => {
  const reg = buildRegistry();
  const rejected = processDocument(reg, { name: "Ada" });
  assert.ok(!rejected.ok);
  assert.equal(rejected.phase, "version-resolution");
  assert.equal(rejected.errors[0].instancePath, "/version");

  const assumed = processDocument(reg, { name: "Ada" }, { assumeVersion: "1.0" });
  assert.ok(assumed.ok);
  assert.equal(assumed.fromVersion, "1.0");
});

test("unknown version is rejected with known versions listed", () => {
  const reg = buildRegistry();
  const res = processDocument(reg, { version: "9.9", name: "x" });
  assert.ok(!res.ok);
  assert.equal(res.phase, "version-resolution");
  assert.match(res.errors[0].message, /unknown version "9\.9"/);
  assert.match(res.errors[0].message, /1\.0, 2\.0/);
});

test("validation errors carry phase, instancePath and schemaPath", () => {
  const reg = buildRegistry();
  const res = processDocument(reg, { version: "1.0", name: "" });
  assert.ok(!res.ok);
  assert.equal(res.phase, "validation");
  const err = res.errors[0];
  assert.equal(err.phase, "validation");
  assert.equal(err.instancePath, "/name");
  assert.equal(err.schemaPath, "#/properties/name/minLength");
});

test("migration failure preserves the original document", () => {
  const reg = new Registry();
  reg.registerSchema("1.0", {
    type: "object",
    properties: { version: { type: "string" } },
    required: ["version"],
  });
  reg.registerSchema("2.0", {
    type: "object",
    properties: { version: { type: "string" } },
    required: ["version"],
  });
  reg.registerMigration("1.0", "2.0", () => {
    throw new Error("corrupt payload");
  });
  const original = { version: "1.0" };
  const res = processDocument(reg, original);
  assert.ok(!res.ok);
  assert.equal(res.phase, "migration");
  assert.match(res.errors[0].message, /corrupt payload/);
  assert.deepEqual(res.document, { version: "1.0" });
  assert.notEqual(res.document, original);
});

test("failed revalidation reports target-schema paths and keeps original", () => {
  const reg = new Registry();
  reg.registerSchema("1.0", {
    type: "object",
    properties: { version: { type: "string" }, n: { type: "integer" } },
    required: ["version", "n"],
  });
  reg.registerSchema("2.0", {
    type: "object",
    properties: { version: { type: "string" }, n: { type: "integer", minimum: 10 } },
    required: ["version", "n"],
  });
  reg.registerMigration("1.0", "2.0", (doc) => ({ ...doc, version: "2.0" }));
  const res = processDocument(reg, { version: "1.0", n: 3 });
  assert.ok(!res.ok);
  assert.equal(res.phase, "revalidation");
  assert.equal(res.errors[0].instancePath, "/n");
  assert.equal(res.errors[0].schemaPath, "#/properties/n/minimum");
  assert.deepEqual(res.document, { version: "1.0", n: 3 });
});

test("no explicit chain means migration phase error", () => {
  const reg = new Registry();
  for (const v of ["1.0", "2.0"]) {
    reg.registerSchema(v, {
      type: "object",
      properties: { version: { type: "string" } },
      required: ["version"],
    });
  }
  const res = processDocument(reg, { version: "1.0" });
  assert.ok(!res.ok);
  assert.equal(res.phase, "migration");
  assert.match(res.errors[0].message, /no explicit migration chain/);
});

test("custom version field is honored", () => {
  const reg = new Registry();
  reg.registerSchema("1.0", {
    type: "object",
    properties: { v: { type: "string" } },
    required: ["v"],
  });
  const res = processDocument(reg, { v: "1.0" }, { versionField: "v" });
  assert.ok(res.ok);
});
