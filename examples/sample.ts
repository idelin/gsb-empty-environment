import { Registry, processDocument, SchemaConflictError } from "../src/index.ts";

const registry = new Registry();

registry.registerSchema("1.0", {
  type: "object",
  properties: {
    version: { type: "string" },
    name: { type: "string", minLength: 1 },
  },
  required: ["version", "name"],
  additionalProperties: false,
});

registry.registerSchema("2.0", {
  type: "object",
  properties: {
    version: { type: "string" },
    fullName: { type: "string", minLength: 1 },
    email: { type: "string", pattern: "^[^@]+@[^@]+$" },
  },
  required: ["version", "fullName", "email"],
  additionalProperties: false,
});

registry.registerMigration("1.0", "2.0", (doc) => {
  const { name, ...rest } = doc;
  return { ...rest, version: "2.0", fullName: name, email: "unknown@example.com" };
});

function show(label: string, result: ReturnType<typeof processDocument>): void {
  console.log(`--- ${label} ---`);
  if (result.ok) {
    console.log(`ok: ${result.fromVersion} -> ${result.toVersion} (migrated: ${result.migrated})`);
    console.log(JSON.stringify(result.document));
  } else {
    console.log(`failed in phase: ${result.phase}`);
    for (const e of result.errors) {
      console.log(`  [${e.keyword}] ${e.message}`);
      console.log(`    instance: ${e.instancePath || "/"}   rule: ${e.schemaPath}`);
    }
    console.log(`  original preserved: ${JSON.stringify(result.document)}`);
  }
  console.log();
}

show("v1.0 doc migrated to v2.0", processDocument(registry, { version: "1.0", name: "Ada" }));
show("v2.0 doc, no migration", processDocument(registry, { version: "2.0", fullName: "Ada", email: "ada@lovelace.dev" }));
show("missing version (rejected)", processDocument(registry, { name: "Ada" }));
show("missing version, explicit assumeVersion", processDocument(registry, { name: "Ada" }, { assumeVersion: "1.0" }));
show("unknown version (rejected)", processDocument(registry, { version: "9.9", name: "x" }));
show("validation error with both paths", processDocument(registry, { version: "1.0", name: "" }));

try {
  registry.registerSchema("1.0", {
    type: "object",
    properties: { version: { type: "string" }, name: { type: "string", minLength: 2 } },
    required: ["version", "name"],
  });
} catch (err) {
  console.log("--- same version, different schema digest ---");
  console.log(err instanceof SchemaConflictError ? err.message : err);
}
