import { Arena } from './arena.ts';
import { PathError, PendingRefError } from './errors.ts';
import { emitNode } from './exporter.ts';
import type { GNode, JsonPrimitive, JsonValue, Key, NodeId, NodeKind } from './model.ts';

export type Probe =
  | { type: 'node'; nodeId: NodeId; kind: NodeKind }
  | { type: 'prim'; value: JsonPrimitive }
  | { type: 'pending-ref'; refId: string };

export type SlotInfo =
  | { type: 'prim'; value: JsonPrimitive }
  | { type: 'child'; nodeId: NodeId }
  | { type: 'ref'; refId: string; status: 'resolved' | 'pending' | 'type-mismatch'; targetId?: NodeId };

// Immutable read view over one version (or one draft state).
export class View {
  readonly arena: Arena;
  readonly roots: ReadonlyMap<string, NodeId>;
  readonly idTable: ReadonlyMap<string, NodeId>;

  constructor(
    arena: Arena,
    roots: ReadonlyMap<string, NodeId>,
    idTable: ReadonlyMap<string, NodeId>,
  ) {
    this.arena = arena;
    this.roots = roots;
    this.idTable = idTable;
  }

  private container(docId: string, keys: Key[]): { node: GNode; path: string } {
    const base = `/docs/${docId}`;
    const rootId = this.roots.get(docId);
    if (rootId === undefined) throw new PathError(`unknown document "${docId}"`, base);
    let node = this.arena.get(rootId);
    let path = base;
    for (const key of keys) {
      const slot = node.slots.get(key);
      if (slot === undefined) throw new PathError(`no such key "${String(key)}"`, path);
      const here = `${path}/${String(key)}`;
      if (slot.t === 'prim') throw new PathError('cannot traverse into a primitive', here);
      let targetId: NodeId;
      if (slot.t === 'child') {
        targetId = slot.node;
      } else {
        const resolved = this.idTable.get(slot.refId);
        if (resolved === undefined) throw new PendingRefError(slot.refId, here);
        targetId = resolved;
      }
      node = this.arena.get(targetId);
      path = here;
    }
    return { node, path };
  }

  // Follows child edges and resolves ref edges; never crosses a pending ref.
  at(docId: string, path: Key[] = []): Probe {
    if (path.length === 0) {
      const rootId = this.roots.get(docId);
      if (rootId === undefined) throw new PathError(`unknown document "${docId}"`, `/docs/${docId}`);
      const node = this.arena.get(rootId);
      return { type: 'node', nodeId: node.id, kind: node.kind };
    }
    const { node, path: parentPath } = this.container(docId, path.slice(0, -1));
    const key = path[path.length - 1];
    const slot = node.slots.get(key);
    if (slot === undefined) throw new PathError(`no such key "${String(key)}"`, parentPath);
    if (slot.t === 'prim') return { type: 'prim', value: slot.v };
    if (slot.t === 'child') {
      const target = this.arena.get(slot.node);
      return { type: 'node', nodeId: target.id, kind: target.kind };
    }
    const resolved = this.idTable.get(slot.refId);
    if (resolved === undefined) return { type: 'pending-ref', refId: slot.refId };
    const target = this.arena.get(resolved);
    return { type: 'node', nodeId: target.id, kind: target.kind };
  }

  // Describes the slot itself without following a ref edge.
  slotInfo(docId: string, path: Key[]): SlotInfo {
    if (path.length === 0) {
      const rootId = this.roots.get(docId);
      if (rootId === undefined) throw new PathError(`unknown document "${docId}"`, `/docs/${docId}`);
      return { type: 'child', nodeId: rootId };
    }
    const { node, path: parentPath } = this.container(docId, path.slice(0, -1));
    const key = path[path.length - 1];
    const slot = node.slots.get(key);
    if (slot === undefined) throw new PathError(`no such key "${String(key)}"`, parentPath);
    if (slot.t === 'prim') return { type: 'prim', value: slot.v };
    if (slot.t === 'child') return { type: 'child', nodeId: slot.node };
    const resolved = this.idTable.get(slot.refId);
    if (resolved === undefined) return { type: 'ref', refId: slot.refId, status: 'pending' };
    const kind = this.arena.get(resolved).kind;
    if (slot.expect !== undefined && slot.expect !== kind) {
      return { type: 'ref', refId: slot.refId, status: 'type-mismatch', targetId: resolved };
    }
    return { type: 'ref', refId: slot.refId, status: 'resolved', targetId: resolved };
  }

  // Materializes a subtree back to plain JSON (shared/cyclic nodes become $id/$ref).
  value(docId: string, path: Key[] = []): JsonValue {
    const probe = this.at(docId, path);
    if (probe.type === 'prim') return probe.value;
    if (probe.type === 'pending-ref') return { $ref: probe.refId };
    return emitNode(this.arena, this.idTable, new Set(), probe.nodeId);
  }

  materialize(docId: string): JsonValue {
    return this.value(docId, []);
  }
}
