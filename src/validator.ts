import type { PropDef, Schema } from "./schema.ts";
import type { ValidationError } from "./errors.ts";
import type { Doc, Incoming } from "./store.ts";
import { toPointer } from "./pointer.ts";

export interface FullValidation {
  errors: ValidationError[];
  incoming: Incoming;
}

interface ObjectFrame {
  docId: string;
  value: Record<string, unknown>;
  props: Record<string, PropDef>;
  required: string[];
  instPath: string[];
  rulePath: string;
}

/**
 * Independent full validator: revalidates every document from scratch and
 * rebuilds the reverse index. Implemented as a separate, stack-based walk
 * (no code shared with the store's commit-time checks beyond schema/error
 * types) so it can be diffed against incremental transaction results.
 */
export function validateAll(docs: Iterable<Doc>, schema: Schema): FullValidation {
  const byId = new Map<string, Doc>();
  for (const doc of docs) byId.set(doc.id, doc);

  const errors: ValidationError[] = [];
  const incoming: Incoming = new Map();

  const recordRef = (target: string, source: string, path: string): void => {
    let inner = incoming.get(target);
    if (!inner) {
      inner = new Map();
      incoming.set(target, inner);
    }
    let paths = inner.get(source);
    if (!paths) {
      paths = new Set();
      inner.set(source, paths);
    }
    paths.add(path);
  };

  const stack: ObjectFrame[] = [];
  for (const doc of byId.values()) {
    const typeDef = schema.types[doc.type];
    if (!typeDef) {
      errors.push({
        code: "UNKNOWN_TYPE",
        docId: doc.id,
        instancePath: "",
        schemaPath: "#/types",
        slice: doc.type,
        message: `unknown type "${doc.type}"`,
      });
      continue;
    }
    stack.push({
      docId: doc.id,
      value: doc.data,
      props: typeDef.properties,
      required: typeDef.required ?? [],
      instPath: [],
      rulePath: `#/types/${doc.type}`,
    });
  }

  while (stack.length > 0) {
    const frame = stack.pop()!;
    const { docId, value, props, required, instPath, rulePath } = frame;

    for (const name of required) {
      if (value[name] === undefined) {
        errors.push({
          code: "MISSING_REQUIRED",
          docId,
          instancePath: toPointer(instPath),
          schemaPath: `${rulePath}/required`,
          slice: value,
          message: `missing required property "${name}"`,
        });
      }
    }

    for (const [name, def] of Object.entries(props)) {
      const v = value[name];
      if (v === undefined) continue;
      const childInst = [...instPath, name];
      const childRule = `${rulePath}/properties/${name}`;
      const pointer = toPointer(childInst);

      if (def.kind === "scalar") {
        if (typeof v !== def.scalar) {
          errors.push({
            code: "TYPE_MISMATCH",
            docId,
            instancePath: pointer,
            schemaPath: childRule,
            slice: v,
            message: `expected ${def.scalar}`,
          });
        }
      } else if (def.kind === "ref" || def.kind === "refArray") {
        const ids: { id: string; path: string[] }[] = [];
        if (def.kind === "ref") {
          if (typeof v !== "string") {
            errors.push({
              code: "TYPE_MISMATCH",
              docId,
              instancePath: pointer,
              schemaPath: childRule,
              slice: v,
              message: "expected a reference id string",
            });
            continue;
          }
          ids.push({ id: v, path: childInst });
        } else {
          if (!Array.isArray(v) || v.some((item) => typeof item !== "string")) {
            errors.push({
              code: "TYPE_MISMATCH",
              docId,
              instancePath: pointer,
              schemaPath: childRule,
              slice: v,
              message: "expected an array of reference id strings",
            });
            continue;
          }
          v.forEach((id, i) => ids.push({ id, path: [...childInst, String(i)] }));
        }
        for (const { id, path } of ids) {
          recordRef(id, docId, toPointer(path));
          const target = byId.get(id);
          if (!target) {
            errors.push({
              code: "REF_NOT_FOUND",
              docId,
              instancePath: toPointer(path),
              schemaPath: childRule,
              slice: id,
              message: `reference to missing document "${id}"`,
            });
          } else if (target.type !== def.target) {
            errors.push({
              code: "REF_WRONG_TYPE",
              docId,
              instancePath: toPointer(path),
              schemaPath: childRule,
              slice: id,
              message: `reference "${id}" has type "${target.type}", expected "${def.target}"`,
            });
          }
        }
      } else {
        if (v === null || typeof v !== "object" || Array.isArray(v)) {
          errors.push({
            code: "TYPE_MISMATCH",
            docId,
            instancePath: pointer,
            schemaPath: childRule,
            slice: v,
            message: "expected an object",
          });
          continue;
        }
        stack.push({
          docId,
          value: v as Record<string, unknown>,
          props: def.properties,
          required: def.required ?? [],
          instPath: childInst,
          rulePath: childRule,
        });
      }
    }
  }

  for (const [targetId, refs] of incoming) {
    const target = byId.get(targetId);
    if (!target) continue;
    const typeDef = schema.types[target.type];
    if (!typeDef || typeDef.maxIncoming === undefined) continue;
    let count = 0;
    for (const paths of refs.values()) count += paths.size;
    if (count > typeDef.maxIncoming) {
      const referrers = [...refs.keys()].sort();
      errors.push({
        code: "MAX_INCOMING_EXCEEDED",
        docId: targetId,
        instancePath: "",
        schemaPath: `#/types/${target.type}/maxIncoming`,
        slice: referrers,
        message: `"${targetId}" has ${count} inbound references, maxIncoming is ${typeDef.maxIncoming}`,
      });
    }
  }

  return { errors, incoming };
}
