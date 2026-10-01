import { SchemaRegistry } from "../src/schema.ts";
import { DocumentStore, type ReverseIndex } from "../src/store.ts";

/** Shared fixture: versioned schemas with refs and reverse cardinality. */
export function makeRegistry(): SchemaRegistry {
  const registry = new SchemaRegistry();
  registry.register({
    name: "user",
    version: 1,
    root: {
      kind: "object",
      required: ["name"],
      properties: {
        name: { kind: "string" },
        friend: { kind: "ref", targets: ["user"] },
      },
    },
  });
  registry.register({
    name: "user",
    version: 2,
    root: {
      kind: "object",
      required: ["name", "email"],
      properties: {
        name: { kind: "string" },
        email: { kind: "string" },
        friend: { kind: "ref", targets: ["user"] },
      },
    },
  });
  registry.register({
    name: "post",
    version: 1,
    root: {
      kind: "object",
      required: ["title", "author"],
      properties: {
        title: { kind: "string" },
        author: { kind: "ref", targets: ["user"] },
        editor: { kind: "ref", targets: ["user"], maxIncoming: 1 },
        tags: { kind: "array", items: { kind: "string" }, maxItems: 3 },
      },
    },
  });
  registry.register({
    name: "comment",
    version: 1,
    root: {
      kind: "object",
      required: ["text", "post"],
      properties: {
        text: { kind: "string" },
        post: { kind: "ref", targets: ["post"] },
        author: { kind: "ref", targets: ["user"] },
      },
    },
  });
  // Versioned target-type rules: v1 only allows users, v2 also allows teams.
  registry.register({
    name: "audit",
    version: 1,
    root: {
      kind: "object",
      required: ["subject"],
      properties: { subject: { kind: "ref", targets: ["user"] } },
    },
  });
  registry.register({
    name: "audit",
    version: 2,
    root: {
      kind: "object",
      required: ["subject"],
      properties: { subject: { kind: "ref", targets: ["user", "team"] } },
    },
  });
  registry.register({
    name: "team",
    version: 1,
    root: {
      kind: "object",
      required: ["name"],
      properties: { name: { kind: "string" } },
    },
  });
  return registry;
}

export function makeStore(): { registry: SchemaRegistry; store: DocumentStore } {
  const registry = makeRegistry();
  return { registry, store: new DocumentStore(registry) };
}

/** Deterministic plain-object form of a reverse index, for cross-checking. */
export function normalizeReverse(reverse: ReverseIndex): Record<string, Record<string, unknown[]>> {
  const out: Record<string, Record<string, unknown[]>> = {};
  for (const target of [...reverse.keys()].sort()) {
    const bySource = reverse.get(target)!;
    const entry: Record<string, unknown[]> = {};
    for (const source of [...bySource.keys()].sort()) {
      entry[source] = bySource
        .get(source)!
        .map((o) => ({
          source: o.source,
          instancePath: o.instancePath,
          rulePath: o.rulePath,
          schemaKey: o.schemaKey,
          target: o.target,
        }))
        .sort((a, b) => (a.instancePath < b.instancePath ? -1 : 1));
    }
    out[target] = entry;
  }
  return out;
}
