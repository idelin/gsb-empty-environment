import { makeError, type ValidationError } from "./errors.ts";
import { SchemaRegistry, walkData, type RefOccurrence } from "./schema.ts";
import type { ReverseIndex, StoredDoc } from "./store.ts";

export interface FullValidationResult {
  errors: ValidationError[];
  /** Reverse index recomputed from scratch, independently of the store. */
  reverse: ReverseIndex;
}

/**
 * Independent full validator. It shares only the schema-walking primitive
 * with the store; all integrity decisions (existence, target type, reverse
 * cardinality, duplicate ids) are re-derived here from the raw document
 * list, so it can be used to cross-check the store's incremental state.
 */
export function validateAll(registry: SchemaRegistry, docs: StoredDoc[]): FullValidationResult {
  const errors: ValidationError[] = [];
  const byId = new Map<string, StoredDoc>();

  for (const doc of docs) {
    const existing = byId.get(doc.id);
    if (existing !== undefined) {
      errors.push(
        makeError(
          "duplicate-id",
          `document id "${doc.id}" is used by two different documents`,
          "",
          "",
          [
            { id: existing.id, path: "#", value: { type: existing.type, schemaVersion: existing.schemaVersion } },
            { id: doc.id, path: "#", value: { type: doc.type, schemaVersion: doc.schemaVersion } },
          ],
        ),
      );
      continue;
    }
    byId.set(doc.id, doc);
  }

  const reverse: ReverseIndex = new Map();
  const allOccs: RefOccurrence[] = [];

  for (const doc of byId.values()) {
    const def = registry.get(doc.type, doc.schemaVersion);
    if (def === undefined) {
      errors.push(
        makeError(
          "unknown-schema",
          `no schema registered for type "${doc.type}" version ${doc.schemaVersion}`,
          "",
          "",
          [{ id: doc.id, path: "#", value: { type: doc.type, schemaVersion: doc.schemaVersion } }],
        ),
      );
      continue;
    }
    const walk = { errors: [] as ValidationError[], refs: [] as RefOccurrence[] };
    walkData(def.root, doc.data, doc.id, SchemaRegistry.key(def.name, def.version), "", "", walk);
    errors.push(...walk.errors);
    allOccs.push(...walk.refs);
  }

  for (const occ of allOccs) {
    let bySource = reverse.get(occ.target);
    if (bySource === undefined) {
      bySource = new Map();
      reverse.set(occ.target, bySource);
    }
    const list = bySource.get(occ.source);
    if (list === undefined) bySource.set(occ.source, [occ]);
    else list.push(occ);
  }

  for (const occ of allOccs) {
    const target = byId.get(occ.target);
    if (target === undefined) {
      errors.push(
        makeError(
          "ref-missing",
          `reference from "${occ.source}" points at missing document "${occ.target}"`,
          occ.instancePath,
          occ.rulePath,
          [{ id: occ.source, path: occ.instancePath, value: occ.target }],
        ),
      );
      continue;
    }
    if (!occ.node.targets.includes(target.type)) {
      errors.push(
        makeError(
          "ref-type",
          `reference from "${occ.source}" expects target type ${occ.node.targets.map((t) => `"${t}"`).join("|")} but "${occ.target}" is "${target.type}"`,
          occ.instancePath,
          occ.rulePath,
          [
            { id: occ.source, path: occ.instancePath, value: occ.target },
            { id: target.id, path: "#", value: { type: target.type, schemaVersion: target.schemaVersion } },
          ],
        ),
      );
    }
  }

  const groups = new Map<string, RefOccurrence[]>();
  for (const occ of allOccs) {
    const key = `${occ.target}${occ.schemaKey}${occ.rulePath}`;
    const list = groups.get(key);
    if (list === undefined) groups.set(key, [occ]);
    else list.push(occ);
  }
  for (const occs of groups.values()) {
    const first = occs[0];
    if (first.node.maxIncoming === undefined) continue;
    const sources = new Set(occs.map((o) => o.source));
    if (sources.size <= first.node.maxIncoming) continue;
    const target = byId.get(first.target);
    errors.push(
      makeError(
        "ref-max-incoming",
        `document "${first.target}" is referenced by ${sources.size} documents through ${first.rulePath}, allowed max is ${first.node.maxIncoming}`,
        "",
        first.rulePath,
        [
          {
            id: first.target,
            path: "#",
            value: target === undefined ? null : { type: target.type, schemaVersion: target.schemaVersion },
          },
          ...occs.map((o) => ({ id: o.source, path: o.instancePath, value: o.target })),
        ],
      ),
    );
  }

  return { errors, reverse };
}
