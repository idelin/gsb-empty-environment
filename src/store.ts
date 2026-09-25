import { Arena } from './arena.ts';
import { GraphError, PathError, PendingRefError, PublishError } from './errors.ts';
import type { Issue } from './errors.ts';
import { emitNode } from './exporter.ts';
import type { ExportResult } from './exporter.ts';
import { buildSlot } from './loader.ts';
import type { BackRef, JsonValue, Key, NodeId } from './model.ts';
import { View } from './view.ts';

export class Version extends View {
  readonly num: number;
  constructor(
    arena: Arena,
    roots: ReadonlyMap<string, NodeId>,
    idTable: ReadonlyMap<string, NodeId>,
    num: number,
  ) {
    super(arena, roots, idTable);
    this.num = num;
  }
}

interface ScanFrame {
  id: NodeId;
  path: string;
}

function rebuildIdTable(
  arena: Arena,
  roots: ReadonlyMap<string, NodeId>,
): { idTable: Map<string, NodeId>; issues: Issue[] } {
  const idTable = new Map<string, NodeId>();
  const issues: Issue[] = [];
  const visited = new Set<NodeId>();
  const stack: ScanFrame[] = [];
  for (const [docId, rootId] of roots) stack.push({ id: rootId, path: `/docs/${docId}` });
  while (stack.length > 0) {
    const { id, path } = stack.pop()!;
    if (visited.has(id)) continue;
    visited.add(id);
    const node = arena.get(id);
    if (node.idName !== undefined) {
      if (idTable.has(node.idName)) {
        issues.push({ code: 'duplicate-id', message: `duplicate $id "${node.idName}"`, path });
      } else {
        idTable.set(node.idName, id);
      }
    }
    for (const [key, slot] of node.slots) {
      if (slot.t === 'child') stack.push({ id: slot.node, path: `${path}/${String(key)}` });
    }
  }
  return { idTable, issues };
}

function validateRefs(
  arena: Arena,
  roots: ReadonlyMap<string, NodeId>,
  idTable: ReadonlyMap<string, NodeId>,
): Issue[] {
  const issues: Issue[] = [];
  const visited = new Set<NodeId>();
  const stack: ScanFrame[] = [];
  for (const [docId, rootId] of roots) stack.push({ id: rootId, path: `/docs/${docId}` });
  while (stack.length > 0) {
    const { id, path } = stack.pop()!;
    if (visited.has(id)) continue;
    visited.add(id);
    const node = arena.get(id);
    for (const [key, slot] of node.slots) {
      const here = `${path}/${String(key)}`;
      if (slot.t === 'child') {
        stack.push({ id: slot.node, path: here });
      } else if (slot.t === 'ref') {
        const target = idTable.get(slot.refId);
        if (target === undefined) {
          issues.push({
            code: 'dangling-ref',
            refId: slot.refId,
            message: `dangling $ref "${slot.refId}"`,
            path: here,
          });
        } else if (slot.expect !== undefined && arena.get(target).kind !== slot.expect) {
          issues.push({
            code: 'type-mismatch',
            refId: slot.refId,
            message: `$ref "${slot.refId}" expects ${slot.expect} but target is ${arena.get(target).kind}`,
            path: here,
          });
        }
      }
    }
  }
  return issues;
}

export class Draft extends View {
  private readonly store: GraphStore;
  private readonly rootsM: Map<string, NodeId>;
  private readonly idsM: Map<string, NodeId>;
  private structuralIssues: Issue[] = [];

  constructor(store: GraphStore, baseNum: number) {
    const base = baseNum === 0 ? undefined : store.version(baseNum);
    const roots = new Map(base?.roots ?? []);
    const ids = new Map(base?.idTable ?? []);
    super(store.arena, roots, ids);
    this.store = store;
    this.rootsM = roots;
    this.idsM = ids;
  }

  private rebuild(): void {
    const { idTable, issues } = rebuildIdTable(this.arena, this.rootsM);
    this.idsM.clear();
    for (const [name, id] of idTable) this.idsM.set(name, id);
    this.structuralIssues = issues;
  }

  addDocument(docId: string, json: JsonValue): void {
    const base = `/docs/${docId}`;
    if (this.rootsM.has(docId)) throw new GraphError(`document "${docId}" already exists`, base);
    const slot = buildSlot(this.arena, json, base, new Set());
    if (slot.t !== 'child') throw new GraphError('document root must be an object or array', base);
    this.rootsM.set(docId, slot.node);
    this.rebuild();
  }

  deleteDocument(docId: string): void {
    const base = `/docs/${docId}`;
    if (!this.rootsM.delete(docId)) throw new PathError(`unknown document "${docId}"`, base);
    this.rebuild();
  }

  set(docId: string, path: Key[], value: JsonValue): void {
    const base = `/docs/${docId}`;
    const rootId = this.rootsM.get(docId);
    if (rootId === undefined) throw new PathError(`unknown document "${docId}"`, base);
    const declared = new Set<string>();
    if (path.length === 0) {
      const slot = buildSlot(this.arena, value, base, declared);
      if (slot.t !== 'child') throw new GraphError('document root must be an object or array', base);
      this.rootsM.set(docId, slot.node);
    } else {
      this.rootsM.set(docId, this.cowSet(rootId, path, 0, value, base, declared));
    }
    this.rebuild();
  }

  // Copy-on-write: clones every container on the path so the base version
  // and sibling roots keep their untouched nodes. Crossing a ref edge
  // materializes a private deep copy of the shared subgraph at the ref
  // site — the other roots keep pointing at the original identity.
  private cowSet(
    nodeId: NodeId,
    keys: Key[],
    i: number,
    value: JsonValue,
    path: string,
    declared: Set<string>,
  ): NodeId {
    const node = this.arena.get(nodeId);
    const key = keys[i];
    const here = `${path}/${String(key)}`;
    const clone = this.arena.cloneShallow(node);
    if (i === keys.length - 1) {
      const slot = buildSlot(this.arena, value, here, declared);
      clone.slots.set(key, slot);
      if (slot.t === 'child') this.arena.linkChild(clone.id, key, slot.node);
      return clone.id;
    }
    const slot = node.slots.get(key);
    if (slot === undefined) throw new PathError(`no such key "${String(key)}"`, path);
    if (slot.t === 'prim') throw new PathError('cannot traverse into a primitive', here);
    let childId: NodeId;
    if (slot.t === 'child') {
      childId = slot.node;
    } else {
      const target = this.idsM.get(slot.refId);
      if (target === undefined) throw new PendingRefError(slot.refId, here);
      childId = this.arena.deepClonePrivate(target);
    }
    const newChild = this.cowSet(childId, keys, i + 1, value, here, declared);
    clone.slots.set(key, { t: 'child', node: newChild });
    this.arena.linkChild(clone.id, key, newChild);
    return clone.id;
  }

  pendingRefs(): { path: string; refId: string }[] {
    return validateRefs(this.arena, this.rootsM, this.idsM)
      .filter((issue) => issue.code === 'dangling-ref')
      .map((issue) => ({ path: issue.path, refId: issue.refId! }));
  }

  // Validates the whole graph; on any issue nothing is committed and the
  // draft's nodes stay unreachable garbage — a full rollback by construction.
  publish(): number {
    const issues = [...this.structuralIssues, ...validateRefs(this.arena, this.rootsM, this.idsM)];
    if (issues.length > 0) throw new PublishError(issues);
    return this.store.commit(
      new Version(this.arena, new Map(this.rootsM), new Map(this.idsM), this.store.current + 1),
    );
  }
}

export class GraphStore {
  readonly arena = new Arena();
  private readonly versions = new Map<number, Version>();
  current = 0;

  load(docs: Record<string, JsonValue>): number {
    if (this.current !== 0) throw new Error('store already loaded; use beginDraft()');
    const draft = this.beginDraft();
    for (const [docId, json] of Object.entries(docs)) draft.addDocument(docId, json);
    return draft.publish();
  }

  beginDraft(): Draft {
    return new Draft(this, this.current);
  }

  commit(version: Version): number {
    this.versions.set(version.num, version);
    this.current = version.num;
    return version.num;
  }

  version(num?: number): Version {
    const n = num ?? this.current;
    const v = this.versions.get(n);
    if (!v) throw new Error(`version ${n} is not retained`);
    return v;
  }

  view(num?: number): View {
    return this.version(num);
  }

  releaseVersion(num: number): void {
    if (num === this.current) throw new Error('cannot release the current version');
    this.versions.delete(num);
  }

  retainedVersions(): number[] {
    return [...this.versions.keys()].sort((a, b) => a - b);
  }

  // Root-pruned export: only subgraphs reachable from the given roots are
  // emitted. A shared node whose defining document is not exported gets
  // inlined at its first encounter with its $id, so the export round-trips.
  export(rootDocIds: string[], num?: number): ExportResult {
    const v = this.version(num);
    const emitted = new Set<NodeId>();
    const docs: Record<string, JsonValue> = {};
    for (const docId of rootDocIds) {
      const rootId = v.roots.get(docId);
      if (rootId === undefined) throw new PathError(`unknown document "${docId}"`, `/docs/${docId}`);
      docs[docId] = emitNode(this.arena, v.idTable, emitted, rootId);
    }
    return { docs };
  }

  // Version-scoped reverse references (child and ref edges) for a node.
  backRefs(nodeId: NodeId, num?: number): BackRef[] {
    const v = this.version(num);
    const out: BackRef[] = [];
    const visited = new Set<NodeId>();
    const stack: NodeId[] = [...v.roots.values()];
    while (stack.length > 0) {
      const id = stack.pop()!;
      if (visited.has(id)) continue;
      visited.add(id);
      const node = this.arena.get(id);
      for (const [key, slot] of node.slots) {
        if (slot.t === 'child') {
          if (slot.node === nodeId) out.push({ from: id, key, via: 'child' });
          stack.push(slot.node);
        } else if (slot.t === 'ref') {
          const target = v.idTable.get(slot.refId);
          if (target !== undefined) {
            if (target === nodeId) out.push({ from: id, key, via: 'ref' });
            stack.push(target);
          }
        }
      }
    }
    return out;
  }

  // Mark-sweep over every retained version. Nodes pinned by old snapshots
  // are never collected; returns the reclaimed (collectable) node set.
  gc(): NodeId[] {
    const reachable = new Set<NodeId>();
    for (const v of this.versions.values()) {
      const stack: NodeId[] = [...v.roots.values()];
      while (stack.length > 0) {
        const id = stack.pop()!;
        if (reachable.has(id)) continue;
        reachable.add(id);
        const node = this.arena.get(id);
        for (const slot of node.slots.values()) {
          if (slot.t === 'child') stack.push(slot.node);
          else if (slot.t === 'ref') {
            const target = v.idTable.get(slot.refId);
            if (target !== undefined) stack.push(target);
          }
        }
      }
    }
    const collected: NodeId[] = [];
    for (const id of this.arena.allIds()) {
      if (!reachable.has(id)) {
        collected.push(id);
        this.arena.remove(id);
      }
    }
    return collected;
  }
}
