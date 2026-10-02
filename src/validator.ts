/**
 * Independent full validator.
 *
 * This module deliberately does NOT reuse the repository's commit-time
 * validation or index builder: it re-derives references, the reverse
 * mapping and every rule verdict from the raw document set alone, so it
 * can be used to cross-check the repository after arbitrary batches.
 */
import type { SchemaRegistry } from "./schema.ts";
import type { ValidationError } from "./errors.ts";
import type { Document } from "./repository.ts";

export interface FullValidationResult {
  errors: ValidationError[];
  /** target id -> (source id -> instance paths), computed independently */
  incoming: Map<string, Map<string, string[]>>;
}

export function validateAll(
  docs: Iterable<Document>,
  schemas: SchemaRegistry,
): FullValidationResult {
  const byId = new Map<string, Document>();
  const errors: ValidationError[] = [];
  const incoming = new Map<string, Map<string, string[]>>();

  const docList = [...docs];
  for (const doc of docList) {
    if (byId.has(doc.id)) {
      errors.push({
        code: "DuplicateId",
        docId: doc.id,
        instancePath: "/id",
        rulePath: "/documents/id",
        message: `duplicate id "${doc.id}" in document set`,
        slice: doc.id,
      });
    }
    byId.set(doc.id, doc);
  }

  // Pass 1: per-document structure + outbound references.
  for (const doc of docList) {
    const schema = schemas.get(doc.schemaVersion);
    if (!schema) {
      errors.push({
        code: "UnknownSchemaVersion",
        docId: doc.id,
        instancePath: "/schemaVersion",
        rulePath: `/schemaVersions/${doc.schemaVersion}`,
        message: `unknown schema version "${doc.schemaVersion}"`,
        slice: doc.schemaVersion,
      });
      continue;
    }
    const typeDef = schema.types[doc.type];
    if (!typeDef) {
      errors.push({
        code: "UnknownType",
        docId: doc.id,
        instancePath: "/type",
        rulePath: `/types/${doc.type}`,
        message: `unknown type "${doc.type}" in schema ${doc.schemaVersion}`,
        slice: doc.type,
      });
      continue;
    }

    for (const [field, def] of Object.entries(typeDef.fields)) {
      const value = doc.data[field];
      const instancePath = `/data/${field}`;
      const rulePath = `/types/${doc.type}/fields/${field}`;

      if (value === undefined) {
        if (def.required) {
          errors.push({
            code: "MissingRequiredField",
            docId: doc.id,
            instancePath,
            rulePath,
            message: `missing required field "${field}"`,
            slice: undefined,
          });
        }
        continue;
      }

      if (def.kind === "string" || def.kind === "number" || def.kind === "boolean") {
        if (typeof value !== def.kind) {
          errors.push({
            code: "FieldTypeMismatch",
            docId: doc.id,
            instancePath,
            rulePath,
            message: `field "${field}" must be ${def.kind}, got ${JSON.stringify(value)}`,
            slice: value,
          });
        }
        continue;
      }

      const refValues: Array<{ v: unknown; path: string }> =
        def.kind === "ref"
          ? [{ v: value, path: instancePath }]
          : Array.isArray(value)
            ? value.map((v, i) => ({ v, path: `${instancePath}/${i}` }))
            : [{ v: value, path: instancePath }];

      if (def.kind === "ref[]" && !Array.isArray(value)) {
        errors.push({
          code: "FieldTypeMismatch",
          docId: doc.id,
          instancePath,
          rulePath,
          message: `field "${field}" must be an array of references`,
          slice: value,
        });
        continue;
      }

      for (const { v, path } of refValues) {
        if (typeof v !== "string") {
          errors.push({
            code: "FieldTypeMismatch",
            docId: doc.id,
            instancePath: path,
            rulePath,
            message: `reference must be a string id, got ${JSON.stringify(v)}`,
            slice: v,
          });
          continue;
        }
        // Record reverse edge regardless of validity, so reverse rules can be checked.
        let bySource = incoming.get(v);
        if (!bySource) {
          bySource = new Map();
          incoming.set(v, bySource);
        }
        const paths = bySource.get(doc.id) ?? [];
        paths.push(path);
        bySource.set(doc.id, paths);

        const target = byId.get(v);
        if (!target) {
          errors.push({
            code: "MissingTarget",
            docId: doc.id,
            instancePath: path,
            rulePath,
            message: `reference points to missing document "${v}"`,
            slice: v,
          });
        } else if (target.type !== def.target) {
          errors.push({
            code: "TargetTypeMismatch",
            docId: doc.id,
            instancePath: path,
            rulePath,
            message:
              `reference expects target type "${def.target}" but "${v}" is of type "${target.type}"`,
            slice: { targetId: target.id, expected: def.target, actual: target.type },
          });
        }
      }
    }
  }

  // Pass 2: reverse cardinality, judged per target document.
  for (const doc of docList) {
    const typeDef = schemas.get(doc.schemaVersion)?.types[doc.type];
    if (!typeDef?.incoming) continue;
    const { min, max } = typeDef.incoming;
    const referrers = incoming.get(doc.id) ?? new Map<string, string[]>();
    let count = 0;
    for (const paths of referrers.values()) count += paths.length;
    if ((min !== undefined && count < min) || (max !== undefined && count > max)) {
      errors.push({
        code: "ReverseCardinality",
        docId: doc.id,
        instancePath: "/id",
        rulePath: `/types/${doc.type}/incoming`,
        message:
          `document "${doc.id}" of type "${doc.type}" has ${count} incoming reference(s),` +
          ` expected ${min ?? 0}..${max ?? "∞"}`,
        slice: [...referrers].map(([sourceId, paths]) => ({ docId: sourceId, paths })),
      });
    }
  }

  return { errors, incoming };
}
