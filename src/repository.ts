import type { FieldDef, SchemaRegistry, TypeDef } from "./schema.ts";
import type { ValidationError } from "./errors.ts";
import { joinPath } from "./paths.ts";

/** A stored JSON document. `id` is globally unique across the repository. */
export interface Document {
  id: string;
  /** Type name, resolved in the document's own schema version. */
  type: string;
  /** Which schema version judges this document (and its outbound ref rules). */
  schemaVersion: string;
  data: Record<string, unknown>;
}

/** Reverse index: target id -> (source id -> set of instance paths in the source doc). */
export type ReverseIndex = Map<string, Map<string, Set<string>>>;

type Op =
  | { op: "put"; doc: Document }
  | { op: "rename"; from: string; to: string }
  | { op: "delete"; id: string };

export type CommitResult = { ok: true } | { ok: false; errors: ValidationError[] };

function cloneDoc(doc: Document): Document {
  return { ...doc, data: structuredClone(doc.data) };
}

function sameDoc(a: Document, b: Document): boolean {
  return (
    a.type === b.type &&
    a.schemaVersion === b.schemaVersion &&
    JSON.stringify(a.data) === JSON.stringify(b.data)
  );
}

export class Repository {
  private docs = new Map<string, Document>();
  private incoming: ReverseIndex = new Map();

  private readonly schemas: SchemaRegistry;

  constructor(schemas: SchemaRegistry) {
    this.schemas = schemas;
  }

  transaction(): Transaction {
    return new Transaction(this);
  }

  /** Convenience: run ops as one atomic batch. */
  batch(run: (tx: Transaction) => void): CommitResult {
    const tx = this.transaction();
    run(tx);
    return tx.commit();
  }

  get(id: string): Document | undefined {
    return this.docs.get(id);
  }

  has(id: string): boolean {
    return this.docs.has(id);
  }

  get size(): number {
    return this.docs.size;
  }

  all(): Document[] {
    return [...this.docs.values()];
  }

  /** Read-only view of the reverse index for one target id. */
  incomingOf(id: string): ReadonlyMap<string, ReadonlySet<string>> {
    return this.incoming.get(id) ?? new Map();
  }

  /** Total number of inbound references to `id`. */
  incomingCount(id: string): number {
    let n = 0;
    for (const paths of this.incomingOf(id).values()) n += paths.size;
    return n;
  }

  /** Internal: swap state + reverse index together (atomic from the outside). */
  applyState(next: Map<string, Document>, nextIncoming: ReverseIndex): void {
    this.docs = next;
    this.incoming = nextIncoming;
  }

  get schemaRegistry(): SchemaRegistry {
    return this.schemas;
  }
}

/**
 * Collects put/rename/delete ops and applies them as one atomic unit:
 * the whole batch validates against the post-batch state, so documents
 * inserted in the same batch may reference each other (including cycles).
 * On any error nothing is applied.
 */
export class Transaction {
  private readonly ops: Op[] = [];

  private readonly repo: Repository;

  constructor(repo: Repository) {
    this.repo = repo;
  }

  /** Insert or replace a document. Two *different* docs with the same id in one batch conflict. */
  put(doc: Document): this {
    this.ops.push({ op: "put", doc: structuredClone(doc) });
    return this;
  }

  /** Rename an id; every reference to it (stored or in-batch) is rewritten. */
  rename(from: string, to: string): this {
    this.ops.push({ op: "rename", from, to });
    return this;
  }

  delete(id: string): this {
    this.ops.push({ op: "delete", id });
    return this;
  }

  commit(): CommitResult {
    const schemas = this.repo.schemaRegistry;
    const errors: ValidationError[] = [];

    // Work on a private copy; the live repository is only touched on success.
    const next = new Map<string, Document>();
    for (const [id, doc] of this.repo.all().map((d) => [d.id, d] as const)) {
      next.set(id, doc);
    }
    const batchPuts = new Map<string, Document>();
    const deleted = new Set<string>();

    const fail = (err: ValidationError) => errors.push(err);

    for (const op of this.ops) {
      if (op.op === "put") {
        const doc = op.doc;
        const prior = batchPuts.get(doc.id);
        if (prior && !sameDoc(prior, doc)) {
          fail({
            code: "DuplicateId",
            docId: doc.id,
            instancePath: "/id",
            rulePath: "/documents/id",
            message: `duplicate id "${doc.id}" with different document content in the same batch`,
            slice: { first: prior.data, second: doc.data },
          });
          continue;
        }
        batchPuts.set(doc.id, doc);
        next.set(doc.id, doc);
        deleted.delete(doc.id);
      } else if (op.op === "rename") {
        const { from, to } = op;
        const doc = next.get(from);
        if (!doc) {
          fail({
            code: "MissingDocument",
            docId: from,
            instancePath: "/id",
            rulePath: "/documents/id",
            message: `cannot rename "${from}": no such document`,
            slice: { from, to },
          });
          continue;
        }
        if (next.has(to)) {
          fail({
            code: "RenameConflict",
            docId: to,
            instancePath: "/id",
            rulePath: "/documents/id",
            message: `cannot rename "${from}" to "${to}": target id already exists`,
            slice: { from, to },
          });
          continue;
        }
        const moved = cloneDoc(doc);
        moved.id = to;
        next.delete(from);
        next.set(to, moved);
        const prior = batchPuts.get(from);
        if (prior) {
          batchPuts.delete(from);
          batchPuts.set(to, moved);
        }
        // Rewrite every reference that points at the old id.
        for (const [id, candidate] of next) {
          if (id === to) continue;
          const rewritten = rewriteRefs(candidate, schemas, from, to);
          if (rewritten) {
            next.set(id, rewritten);
            if (batchPuts.has(id)) batchPuts.set(id, rewritten);
          }
        }
      } else {
        const { id } = op;
        if (!next.has(id)) {
          fail({
            code: "MissingDocument",
            docId: id,
            instancePath: "/id",
            rulePath: "/documents/id",
            message: `cannot delete "${id}": no such document`,
            slice: { id },
          });
          continue;
        }
        next.delete(id);
        batchPuts.delete(id);
        deleted.add(id);
      }
    }

    // Structural + referential validation of the post-batch state.
    // MissingTarget errors caused solely by ids deleted in this batch are
    // suppressed: the DeleteReferenced error below reports them precisely.
    const incoming = buildReverseIndex(next, schemas);
    validateState(next, incoming, schemas, errors, deleted);

    // A deleted document that surviving documents still reference is rejected.
    for (const id of deleted) {
      const referrers = incoming.get(id);
      if (referrers && referrers.size > 0) {
        errors.push({
          code: "DeleteReferenced",
          docId: id,
          instancePath: "/id",
          rulePath: "/documents/id",
          message: `cannot delete "${id}": still referenced by ${referrers.size} document(s)`,
          slice: refererSlice(referrers),
        });
      }
    }

    if (errors.length > 0) return { ok: false, errors };

    this.repo.applyState(next, incoming);
    return { ok: true };
  }
}

function refererSlice(referrers: Map<string, Set<string>>): unknown {
  return [...referrers].map(([sourceId, paths]) => ({
    docId: sourceId,
    paths: [...paths],
  }));
}

/** Rewrite schema-declared references from `from` to `to`. Returns undefined if unchanged. */
function rewriteRefs(
  doc: Document,
  schemas: SchemaRegistry,
  from: string,
  to: string,
): Document | undefined {
  const typeDef = schemas.get(doc.schemaVersion)?.types[doc.type];
  if (!typeDef) return undefined;
  let changed = false;
  const data = structuredClone(doc.data);
  for (const [field, def] of Object.entries(typeDef.fields)) {
    const value = data[field];
    if (def.kind === "ref" && value === from) {
      data[field] = to;
      changed = true;
    } else if (def.kind === "ref[]" && Array.isArray(value)) {
      const nextArr = value.map((v) => (v === from ? to : v));
      if (nextArr.some((v, i) => v !== value[i])) {
        data[field] = nextArr;
        changed = true;
      }
    }
  }
  return changed ? { ...doc, data } : undefined;
}

/** Extract every outbound reference of a document as (instancePath, targetId). */
export function extractRefs(
  doc: Document,
  schemas: SchemaRegistry,
): Array<{ path: string; target: string }> {
  const typeDef = schemas.get(doc.schemaVersion)?.types[doc.type];
  if (!typeDef) return [];
  const refs: Array<{ path: string; target: string }> = [];
  for (const [field, def] of Object.entries(typeDef.fields)) {
    const value = doc.data[field];
    const base = joinPath("/data", field);
    if (def.kind === "ref" && typeof value === "string") {
      refs.push({ path: base, target: value });
    } else if (def.kind === "ref[]" && Array.isArray(value)) {
      value.forEach((v, i) => {
        if (typeof v === "string") refs.push({ path: joinPath(base, i), target: v });
      });
    }
  }
  return refs;
}

/** Build the reverse index for a candidate state (target -> source -> paths). */
export function buildReverseIndex(
  docs: Map<string, Document>,
  schemas: SchemaRegistry,
): ReverseIndex {
  const index: ReverseIndex = new Map();
  for (const doc of docs.values()) {
    for (const ref of extractRefs(doc, schemas)) {
      let bySource = index.get(ref.target);
      if (!bySource) {
        bySource = new Map();
        index.set(ref.target, bySource);
      }
      let paths = bySource.get(doc.id);
      if (!paths) {
        paths = new Set();
        bySource.set(doc.id, paths);
      }
      paths.add(ref.path);
    }
  }
  return index;
}

function countIncoming(incoming: ReverseIndex, id: string): number {
  let n = 0;
  for (const paths of incoming.get(id)?.values() ?? []) n += paths.size;
  return n;
}

/** Full structural + referential validation of a candidate state (used at commit time). */
function validateState(
  docs: Map<string, Document>,
  incoming: ReverseIndex,
  schemas: SchemaRegistry,
  errors: ValidationError[],
  deleted: ReadonlySet<string> = new Set(),
): void {
  for (const doc of docs.values()) {
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
    const typeDef: TypeDef | undefined = schema.types[doc.type];
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
      validateField(doc, field, def, docs, errors, deleted);
    }
    // Reverse cardinality: judged on the target document, rule lives on its type.
    if (typeDef.incoming) {
      const count = countIncoming(incoming, doc.id);
      const { min, max } = typeDef.incoming;
      if ((min !== undefined && count < min) || (max !== undefined && count > max)) {
        errors.push({
          code: "ReverseCardinality",
          docId: doc.id,
          instancePath: "/id",
          rulePath: `/types/${doc.type}/incoming`,
          message:
            `document "${doc.id}" of type "${doc.type}" has ${count} incoming reference(s),` +
            ` expected ${min ?? 0}..${max ?? "∞"}`,
          slice: refererSlice(incoming.get(doc.id) ?? new Map()),
        });
      }
    }
  }
}

function validateField(
  doc: Document,
  field: string,
  def: FieldDef,
  docs: Map<string, Document>,
  errors: ValidationError[],
  deleted: ReadonlySet<string>,
): void {
  const value = doc.data[field];
  const instancePath = joinPath("/data", field);
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
    return;
  }

  switch (def.kind) {
    case "string":
    case "number":
    case "boolean":
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
      return;
    case "ref":
      checkRef(doc, instancePath, rulePath, value, def.target, docs, errors, deleted);
      return;
    case "ref[]":
      if (!Array.isArray(value)) {
        errors.push({
          code: "FieldTypeMismatch",
          docId: doc.id,
          instancePath,
          rulePath,
          message: `field "${field}" must be an array of references`,
          slice: value,
        });
        return;
      }
      value.forEach((v, i) =>
        checkRef(doc, joinPath(instancePath, i), rulePath, v, def.target, docs, errors, deleted),
      );
      return;
  }
}

function checkRef(
  doc: Document,
  instancePath: string,
  rulePath: string,
  value: unknown,
  expectedType: string,
  docs: Map<string, Document>,
  errors: ValidationError[],
  deleted: ReadonlySet<string>,
): void {
  if (typeof value !== "string") {
    errors.push({
      code: "FieldTypeMismatch",
      docId: doc.id,
      instancePath,
      rulePath,
      message: `reference must be a string id, got ${JSON.stringify(value)}`,
      slice: value,
    });
    return;
  }
  const target = docs.get(value);
  if (!target) {
    if (deleted.has(value)) return; // reported as DeleteReferenced instead
    errors.push({
      code: "MissingTarget",
      docId: doc.id,
      instancePath,
      rulePath,
      message: `reference points to missing document "${value}"`,
      slice: value,
    });
    return;
  }
  if (target.type !== expectedType) {
    errors.push({
      code: "TargetTypeMismatch",
      docId: doc.id,
      instancePath,
      rulePath,
      message:
        `reference expects target type "${expectedType}" but "${value}" is of type "${target.type}"`,
      slice: { targetId: target.id, expected: expectedType, actual: target.type },
    });
  }
}
