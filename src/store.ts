import type { PropDef, Schema, TypeDef } from "./schema.ts";
import type { ValidationError } from "./errors.ts";
import { toPointer } from "./pointer.ts";

export interface Doc {
  id: string;
  type: string;
  data: Record<string, unknown>;
}

export type Op =
  | { op: "insert"; doc: Doc }
  | { op: "rename"; from: string; to: string }
  | { op: "delete"; id: string };

export type TxnResult = { ok: true } | { ok: false; errors: ValidationError[] };

/** Reverse index: target id -> (referrer doc id -> set of instance paths). */
export type Incoming = Map<string, Map<string, Set<string>>>;

export interface RefSite {
  target: string;
  path: string[];
}

function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) {
    return false;
  }
  if (Array.isArray(a) || Array.isArray(b)) {
    return (
      Array.isArray(a) &&
      Array.isArray(b) &&
      a.length === b.length &&
      a.every((v, i) => deepEqual(v, (b as unknown[])[i]))
    );
  }
  const aObj = a as Record<string, unknown>;
  const bObj = b as Record<string, unknown>;
  const aKeys = Object.keys(aObj);
  const bKeys = Object.keys(bObj);
  return aKeys.length === bKeys.length && aKeys.every((k) => deepEqual(aObj[k], bObj[k]));
}

function cloneIncoming(incoming: Incoming): Incoming {
  const copy: Incoming = new Map();
  for (const [target, refs] of incoming) {
    const inner = new Map<string, Set<string>>();
    for (const [source, paths] of refs) inner.set(source, new Set(paths));
    copy.set(target, inner);
  }
  return copy;
}

function addIncoming(incoming: Incoming, target: string, source: string, path: string): void {
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
}

function removeIncomingSource(incoming: Incoming, target: string, source: string): void {
  const inner = incoming.get(target);
  if (!inner) return;
  inner.delete(source);
  if (inner.size === 0) incoming.delete(target);
}

/** Collect every reference site (target id + path) inside a document. */
export function collectRefs(doc: Doc, schema: Schema): RefSite[] {
  const sites: RefSite[] = [];
  const typeDef = schema.types[doc.type];
  if (!typeDef) return sites;
  const walk = (value: unknown, props: Record<string, PropDef>, path: string[]): void => {
    if (value === null || typeof value !== "object" || Array.isArray(value)) return;
    const obj = value as Record<string, unknown>;
    for (const [name, def] of Object.entries(props)) {
      const v = obj[name];
      if (v === undefined) continue;
      const p = [...path, name];
      if (def.kind === "ref" && typeof v === "string") {
        sites.push({ target: v, path: p });
      } else if (def.kind === "refArray" && Array.isArray(v)) {
        v.forEach((item, i) => {
          if (typeof item === "string") sites.push({ target: item, path: [...p, String(i)] });
        });
      } else if (def.kind === "object") {
        walk(v, def.properties, p);
      }
    }
  };
  walk(doc.data, typeDef.properties, []);
  return sites;
}

/** Replace the reference value stored at `path` when it equals `from`. */
function rewriteRefAt(data: Record<string, unknown>, path: string[], from: string, to: string): void {
  let current: unknown = data;
  for (let i = 0; i < path.length - 1; i++) {
    if (current === null || typeof current !== "object") return;
    current = (current as Record<string, unknown>)[path[i]];
  }
  if (current === null || typeof current !== "object") return;
  const obj = current as Record<string, unknown>;
  const last = path[path.length - 1];
  if (obj[last] === from) obj[last] = to;
}

export class Store {
  schema: Schema;
  docs: Map<string, Doc>;
  incoming: Incoming;

  constructor(schema: Schema) {
    this.schema = schema;
    this.docs = new Map();
    this.incoming = new Map();
  }

  get(id: string): Doc | undefined {
    return this.docs.get(id);
  }

  /** Atomically swap the schema version; rejected when current docs violate it. */
  setSchema(schema: Schema): TxnResult {
    const errors = checkAll(this.docs, this.incoming, schema);
    if (errors.length > 0) return { ok: false, errors };
    this.schema = schema;
    return { ok: true };
  }

  /**
   * Apply a batch of ops atomically. Refs between documents inserted in the
   * same batch resolve (mutual/cyclic references are legal). On any error
   * nothing is committed.
   */
  transact(ops: Op[]): TxnResult {
    const docs = new Map(this.docs);
    const incoming = cloneIncoming(this.incoming);
    const errors: ValidationError[] = [];

    for (const op of ops) {
      if (op.op === "insert") this.applyInsert(docs, incoming, op.doc, errors);
      else if (op.op === "rename") this.applyRename(docs, incoming, op.from, op.to, errors);
      else this.applyDelete(docs, incoming, op.id, errors);
    }
    if (errors.length > 0) return { ok: false, errors };

    let semantic = checkAll(docs, incoming, this.schema);

    // A dangling reference caused by deleting a still-referenced document in
    // this batch is reported once, as DELETE_REFERENCED on the deleted id.
    const deletedIds = new Set(
      ops.filter((o) => o.op === "delete").map((o) => (o as { id: string }).id),
    );
    if (deletedIds.size > 0) {
      const dangling = semantic.filter(
        (e) => e.code === "REF_NOT_FOUND" && deletedIds.has(e.slice as string),
      );
      if (dangling.length > 0) {
        semantic = semantic.filter((e) => !dangling.includes(e));
        for (const id of deletedIds) {
          const refs = incoming.get(id);
          if (refs && refs.size > 0) {
            semantic.push({
              code: "DELETE_REFERENCED",
              docId: id,
              instancePath: "",
              schemaPath: "#",
              slice: [...refs.keys()].sort(),
              message: `cannot delete "${id}": still referenced by ${[...refs.keys()].sort().join(", ")}`,
            });
          }
        }
      }
    }

    if (semantic.length > 0) return { ok: false, errors: semantic };

    // Atomic commit: documents and reverse index swap together.
    this.docs = docs;
    this.incoming = incoming;
    return { ok: true };
  }

  private applyInsert(docs: Map<string, Doc>, incoming: Incoming, doc: Doc, errors: ValidationError[]): void {
    const existing = docs.get(doc.id);
    if (existing) {
      if (deepEqual(existing, doc)) return; // identical re-insert is a no-op
      errors.push({
        code: "DUPLICATE_ID",
        docId: doc.id,
        instancePath: "",
        schemaPath: "#",
        slice: doc,
        message: `duplicate id "${doc.id}" with different content`,
      });
      return;
    }
    if (!this.schema.types[doc.type]) {
      errors.push({
        code: "UNKNOWN_TYPE",
        docId: doc.id,
        instancePath: "",
        schemaPath: "#/types",
        slice: doc.type,
        message: `unknown type "${doc.type}"`,
      });
      return;
    }
    const stored = structuredClone(doc);
    docs.set(doc.id, stored);
    for (const site of collectRefs(stored, this.schema)) {
      addIncoming(incoming, site.target, stored.id, toPointer(site.path));
    }
  }

  private applyRename(docs: Map<string, Doc>, incoming: Incoming, from: string, to: string, errors: ValidationError[]): void {
    if (from === to) return;
    const doc = docs.get(from);
    if (!doc) {
      errors.push({
        code: "NOT_FOUND",
        docId: from,
        instancePath: "",
        schemaPath: "#",
        slice: from,
        message: `cannot rename: "${from}" does not exist`,
      });
      return;
    }
    if (docs.has(to)) {
      errors.push({
        code: "RENAME_CONFLICT",
        docId: to,
        instancePath: "",
        schemaPath: "#",
        slice: to,
        message: `cannot rename "${from}" to "${to}": target id already exists`,
      });
      return;
    }
    docs.delete(from);
    docs.set(to, { ...doc, id: to });

    const referrers = incoming.get(from);
    if (referrers) {
      for (const [sourceId, paths] of referrers) {
        const key = sourceId === from ? to : sourceId;
        const source = docs.get(key);
        if (!source) continue;
        const cloned = structuredClone(source);
        for (const pointer of paths) {
          const segments = pointer.split("/").slice(1).map(unescapeSegment);
          rewriteRefAt(cloned.data, segments, from, to);
        }
        docs.set(key, cloned);
      }
      incoming.delete(from);
      incoming.set(to, referrers);
    }

    // The renamed document may itself be a referrer elsewhere in the index;
    // rewrite its id wherever it appears as a source (including self-refs).
    for (const refs of incoming.values()) {
      const paths = refs.get(from);
      if (paths) {
        refs.delete(from);
        refs.set(to, paths);
      }
    }
  }

  private applyDelete(docs: Map<string, Doc>, incoming: Incoming, id: string, errors: ValidationError[]): void {
    const doc = docs.get(id);
    if (!doc) {
      errors.push({
        code: "NOT_FOUND",
        docId: id,
        instancePath: "",
        schemaPath: "#",
        slice: id,
        message: `cannot delete: "${id}" does not exist`,
      });
      return;
    }
    docs.delete(id);
    for (const site of collectRefs(doc, this.schema)) {
      removeIncomingSource(incoming, site.target, id);
    }
    // Inbound references to `id` are left in the index; the final check turns
    // them into DELETE_REFERENCED errors unless the referrers were also
    // removed (or rewired) inside the same batch.
  }
}

function unescapeSegment(segment: string): string {
  return segment.replace(/~1/g, "/").replace(/~0/g, "~");
}

/**
 * The store's own full-state check, run on the working copy before commit and
 * by setSchema. Deliberately separate from src/validator.ts so the independent
 * validator can cross-check it.
 */
export function checkAll(docs: Map<string, Doc>, incoming: Incoming, schema: Schema): ValidationError[] {
  const errors: ValidationError[] = [];

  for (const doc of docs.values()) {
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
    checkObject(doc.id, doc.data, typeDef.properties, typeDef.required ?? [], [], `#/types/${doc.type}`, docs, schema, errors);
  }

  for (const [targetId, refs] of incoming) {
    const target = docs.get(targetId);
    if (!target) continue; // dangling inbound refs are reported as REF_NOT_FOUND at the referrer
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

  return errors;
}

function checkObject(
  docId: string,
  obj: Record<string, unknown>,
  props: Record<string, PropDef>,
  required: string[],
  instPath: string[],
  rulePath: string,
  docs: Map<string, Doc>,
  schema: Schema,
  errors: ValidationError[],
): void {
  for (const name of required) {
    if (obj[name] === undefined) {
      errors.push({
        code: "MISSING_REQUIRED",
        docId,
        instancePath: toPointer(instPath),
        schemaPath: `${rulePath}/required`,
        slice: obj,
        message: `missing required property "${name}"`,
      });
    }
  }
  for (const [name, def] of Object.entries(props)) {
    const value = obj[name];
    if (value === undefined) continue;
    checkValue(docId, value, def, [...instPath, name], `${rulePath}/properties/${name}`, docs, schema, errors);
  }
}

function checkValue(
  docId: string,
  value: unknown,
  def: PropDef,
  instPath: string[],
  rulePath: string,
  docs: Map<string, Doc>,
  schema: Schema,
  errors: ValidationError[],
): void {
  const instancePath = toPointer(instPath);
  const mismatch = (expected: string): void => {
    errors.push({
      code: "TYPE_MISMATCH",
      docId,
      instancePath,
      schemaPath: rulePath,
      slice: value,
      message: `expected ${expected}`,
    });
  };

  const checkRefTarget = (id: string, path: string[], expectedType: string): void => {
    const target = docs.get(id);
    const pointer = toPointer(path);
    if (!target) {
      errors.push({
        code: "REF_NOT_FOUND",
        docId,
        instancePath: pointer,
        schemaPath: rulePath,
        slice: id,
        message: `reference to missing document "${id}"`,
      });
    } else if (target.type !== expectedType) {
      errors.push({
        code: "REF_WRONG_TYPE",
        docId,
        instancePath: pointer,
        schemaPath: rulePath,
        slice: id,
        message: `reference "${id}" has type "${target.type}", expected "${expectedType}"`,
      });
    }
  };

  switch (def.kind) {
    case "scalar":
      if (typeof value !== def.scalar) mismatch(def.scalar);
      return;
    case "ref":
      if (typeof value !== "string") {
        mismatch("a reference id string");
        return;
      }
      checkRefTarget(value, instPath, def.target);
      return;
    case "refArray": {
      if (!Array.isArray(value) || value.some((v) => typeof v !== "string")) {
        mismatch("an array of reference id strings");
        return;
      }
      value.forEach((id, i) => checkRefTarget(id, [...instPath, String(i)], def.target));
      return;
    }
    case "object": {
      if (value === null || typeof value !== "object" || Array.isArray(value)) {
        mismatch("an object");
        return;
      }
      checkObject(docId, value as Record<string, unknown>, def.properties, def.required ?? [], instPath, rulePath, docs, schema, errors);
      return;
    }
  }
}
