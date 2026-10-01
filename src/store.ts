import { makeError, type ValidationError } from "./errors.ts";
import {
  SchemaRegistry,
  walkData,
  nodeAt,
  type RefOccurrence,
  type SchemaDef,
} from "./schema.ts";

export interface DocInput {
  id: string;
  type: string;
  /** Pinned schema version; defaults to the latest registered version. */
  schemaVersion?: number;
  data: unknown;
}

export interface StoredDoc {
  id: string;
  type: string;
  schemaVersion: number;
  data: unknown;
}

export type Op =
  | { op: "insert"; doc: DocInput }
  | { op: "rename"; id: string; newId: string }
  | { op: "delete"; id: string };

export interface TxResult {
  ok: boolean;
  errors: ValidationError[];
}

/** Reverse index: target id -> source id -> occurrences of the reference. */
export type ReverseIndex = Map<string, Map<string, RefOccurrence[]>>;

function cloneReverse(src: ReverseIndex): ReverseIndex {
  const out: ReverseIndex = new Map();
  for (const [target, bySource] of src) {
    const m = new Map<string, RefOccurrence[]>();
    for (const [source, occs] of bySource) m.set(source, occs.map((o) => ({ ...o })));
    out.set(target, m);
  }
  return out;
}

function removeSource(reverse: ReverseIndex, sourceId: string): void {
  for (const [target, bySource] of reverse) {
    bySource.delete(sourceId);
    if (bySource.size === 0) reverse.delete(target);
  }
}

function addOccurrences(reverse: ReverseIndex, occs: RefOccurrence[]): void {
  for (const occ of occs) {
    let bySource = reverse.get(occ.target);
    if (bySource === undefined) {
      bySource = new Map();
      reverse.set(occ.target, bySource);
    }
    const list = bySource.get(occ.source);
    if (list === undefined) bySource.set(occ.source, [occ]);
    else list.push(occ);
  }
}

/** Set the value at a JSON pointer inside a (already cloned) document tree. */
function setPointer(root: unknown, pointer: string, value: unknown): void {
  const segs = pointer.split("/").slice(1).map((s) => s.replace(/~1/g, "/").replace(/~0/g, "~"));
  let node = root as Record<string, unknown> | unknown[];
  for (let i = 0; i < segs.length - 1; i++) {
    node = (node as Record<string, unknown>)[segs[i]] as Record<string, unknown>;
  }
  (node as Record<string, unknown>)[segs[segs.length - 1]] = value;
}

/**
 * Multi-document store. All mutations happen inside `transact`, which applies
 * a batch of ops atomically: documents and the reverse index are committed
 * together, and any error discards the whole batch.
 */
export class DocumentStore {
  private docs = new Map<string, StoredDoc>();
  private reverse: ReverseIndex = new Map();
  private readonly registry: SchemaRegistry;

  constructor(registry: SchemaRegistry) {
    this.registry = registry;
  }

  get(id: string): StoredDoc | undefined {
    return this.docs.get(id);
  }

  has(id: string): boolean {
    return this.docs.has(id);
  }

  get size(): number {
    return this.docs.size;
  }

  snapshot(): StoredDoc[] {
    return [...this.docs.values()].map((d) => ({ ...d }));
  }

  /** Read-only view of the live reverse index (for cross-checking). */
  reverseIndex(): ReverseIndex {
    return cloneReverse(this.reverse);
  }

  incoming(id: string): RefOccurrence[] {
    const bySource = this.reverse.get(id);
    if (bySource === undefined) return [];
    return [...bySource.values()].flat();
  }

  transact(ops: Op[]): TxResult {
    const workingDocs = new Map(this.docs);
    const workingReverse = cloneReverse(this.reverse);
    const touched = new Set<string>();
    const deleted = new Set<string>();
    const errors: ValidationError[] = [];

    // ---- Phase 1: structural application, in op order ----
    for (const op of ops) {
      if (op.op === "insert") {
        const { id, type } = op.doc;
        if (workingDocs.has(id)) {
          errors.push(
            makeError("duplicate-id", `document id "${id}" already exists`, "", "", [
              { id, path: "#", value: { existingType: workingDocs.get(id)!.type } },
              { id, path: "#", value: { rejectedType: type } },
            ]),
          );
          continue;
        }
        const schema = this.registry.get(type, op.doc.schemaVersion);
        if (schema === undefined) {
          errors.push(
            makeError(
              "unknown-schema",
              `no schema registered for type "${type}"${op.doc.schemaVersion !== undefined ? ` version ${op.doc.schemaVersion}` : ""}`,
              "",
              "",
              [{ id, path: "#", value: { type, schemaVersion: op.doc.schemaVersion ?? null } }],
            ),
          );
          continue;
        }
        workingDocs.set(id, {
          id,
          type,
          schemaVersion: schema.version,
          data: structuredClone(op.doc.data),
        });
        touched.add(id);
      } else if (op.op === "rename") {
        const doc = workingDocs.get(op.id);
        if (doc === undefined) {
          errors.push(
            makeError("rename-missing", `cannot rename missing document "${op.id}"`, "", "", [
              { id: op.id, path: "#", value: null },
            ]),
          );
          continue;
        }
        if (workingDocs.has(op.newId)) {
          errors.push(
            makeError("duplicate-id", `cannot rename "${op.id}": id "${op.newId}" already exists`, "", "", [
              { id: op.newId, path: "#", value: { existingType: workingDocs.get(op.newId)!.type } },
            ]),
          );
          continue;
        }
        workingDocs.delete(op.id);
        workingDocs.set(op.newId, { ...doc, id: op.newId });
        touched.add(op.newId);
        // Outgoing occurrences were recorded under the old id; drop them now
        // (the renamed doc is touched, so they are recomputed in phase 2).
        removeSource(workingReverse, op.id);
        // Rewrite every reference that points at the old id (reference graph
        // follows the rename inside the same transaction).
        for (const [otherId, other] of workingDocs) {
          const schema = this.registry.get(other.type, other.schemaVersion);
          if (schema === undefined) continue;
          const walk = { errors: [], refs: [] } as { errors: ValidationError[]; refs: RefOccurrence[] };
          walkData(schema.root, other.data, otherId, SchemaRegistry.key(other.type, other.schemaVersion), "", "", walk);
          const hits = walk.refs.filter((r) => r.target === op.id);
          if (hits.length === 0) continue;
          const data = structuredClone(other.data);
          for (const hit of hits) setPointer(data, hit.instancePath, op.newId);
          workingDocs.set(otherId, { ...other, data });
          touched.add(otherId);
        }
      } else {
        if (!workingDocs.has(op.id)) {
          errors.push(
            makeError("delete-missing", `cannot delete missing document "${op.id}"`, "", "", [
              { id: op.id, path: "#", value: null },
            ]),
          );
          continue;
        }
        workingDocs.delete(op.id);
        deleted.add(op.id);
        touched.delete(op.id);
      }
    }
    if (errors.length > 0) return { ok: false, errors };

    // ---- Phase 2: re-validate touched docs, maintain reverse index ----
    const newRefsByDoc = new Map<string, RefOccurrence[]>();
    const affectedTargets = new Set<string>();
    for (const id of touched) {
      const old = this.reverseSourcesOf(id);
      for (const occ of old) affectedTargets.add(occ.target);
      removeSource(workingReverse, id);
      const doc = workingDocs.get(id);
      if (doc === undefined) continue;
      const schema = this.registry.get(doc.type, doc.schemaVersion) as SchemaDef;
      const walk = { errors: [] as ValidationError[], refs: [] as RefOccurrence[] };
      walkData(schema.root, doc.data, id, SchemaRegistry.key(doc.type, doc.schemaVersion), "", "", walk);
      errors.push(...walk.errors);
      newRefsByDoc.set(id, walk.refs);
      addOccurrences(workingReverse, walk.refs);
      for (const occ of walk.refs) affectedTargets.add(occ.target);
    }
    for (const id of deleted) {
      removeSource(workingReverse, id);
    }

    // ---- Phase 3: reference checks over the post-batch state ----
    for (const [id, refs] of newRefsByDoc) {
      for (const occ of refs) {
        const target = workingDocs.get(occ.target);
        if (target === undefined) {
          errors.push(
            makeError(
              "ref-missing",
              `reference from "${id}" points at missing document "${occ.target}"`,
              occ.instancePath,
              occ.rulePath,
              [{ id, path: occ.instancePath, value: occ.target }],
            ),
          );
          continue;
        }
        if (!occ.node.targets.includes(target.type)) {
          errors.push(
            makeError(
              "ref-type",
              `reference from "${id}" expects target type ${occ.node.targets.map((t) => `"${t}"`).join("|")} but "${occ.target}" is "${target.type}"`,
              occ.instancePath,
              occ.rulePath,
              [
                { id, path: occ.instancePath, value: occ.target },
                { id: occ.target, path: "#", value: { type: target.type, schemaVersion: target.schemaVersion } },
              ],
            ),
          );
        }
      }
    }

    // Reverse cardinality: per ref rule node, at most maxIncoming distinct sources.
    for (const target of affectedTargets) {
      const bySource = workingReverse.get(target);
      if (bySource === undefined) continue;
      const groups = new Map<string, RefOccurrence[]>();
      for (const occs of bySource.values()) {
        for (const occ of occs) {
          const key = `${occ.schemaKey}|${occ.rulePath}`;
          const list = groups.get(key);
          if (list === undefined) groups.set(key, [occ]);
          else list.push(occ);
        }
      }
      for (const [key, occs] of groups) {
        const [schemaKey, rulePath] = key.split("|");
        const def = this.registry.get(schemaKey.slice(0, schemaKey.lastIndexOf("@")), Number(schemaKey.slice(schemaKey.lastIndexOf("@") + 1)));
        const node = def === undefined ? undefined : nodeAt(def.root, rulePath);
        if (node === undefined || node.kind !== "ref" || node.maxIncoming === undefined) continue;
        const sources = new Set(occs.map((o) => o.source));
        if (sources.size > node.maxIncoming) {
          const targetDoc = workingDocs.get(target);
          errors.push(
            makeError(
              "ref-max-incoming",
              `document "${target}" is referenced by ${sources.size} documents through ${rulePath}, allowed max is ${node.maxIncoming}`,
              "",
              rulePath,
              [
                { id: target, path: "#", value: targetDoc === undefined ? null : { type: targetDoc.type } },
                ...occs.map((o) => ({ id: o.source, path: o.instancePath, value: o.target })),
              ],
            ),
          );
        }
      }
    }

    // Delete check: a deleted document must not remain referenced by survivors.
    for (const id of deleted) {
      const bySource = workingReverse.get(id);
      if (bySource === undefined) continue;
      const dangling = [...bySource.values()].flat().filter((o) => workingDocs.has(o.source));
      if (dangling.length > 0) {
        errors.push(
          makeError(
            "delete-referenced",
            `cannot delete "${id}": still referenced by ${[...new Set(dangling.map((o) => o.source))].map((s) => `"${s}"`).join(", ")}`,
            "",
            "",
            [
              { id, path: "#", value: null },
              ...dangling.map((o) => ({ id: o.source, path: o.instancePath, value: o.target })),
            ],
          ),
        );
      }
    }

    // ---- Phase 4: commit or discard ----
    if (errors.length > 0) return { ok: false, errors };
    this.docs = workingDocs;
    this.reverse = workingReverse;
    return { ok: true, errors: [] };
  }

  private reverseSourcesOf(sourceId: string): RefOccurrence[] {
    const out: RefOccurrence[] = [];
    for (const bySource of this.reverse.values()) {
      const list = bySource.get(sourceId);
      if (list !== undefined) out.push(...list);
    }
    return out;
  }
}
