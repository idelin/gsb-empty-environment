import type { BackRef, GNode, Key, NodeId, NodeKind } from './model.ts';

// Append-only arena of immutable graph nodes. Identity is the NodeId:
// two nodes with identical content are still distinct identities.
export class Arena {
  private nodes = new Map<NodeId, GNode>();
  private seq = 0;

  create(kind: NodeKind, idName?: string): GNode {
    const node: GNode = { id: ++this.seq, kind, slots: new Map(), inbound: [] };
    if (idName !== undefined) node.idName = idName;
    this.nodes.set(node.id, node);
    return node;
  }

  get(id: NodeId): GNode {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`arena node #${id} is gone (collected or never existed)`);
    return node;
  }

  has(id: NodeId): boolean {
    return this.nodes.has(id);
  }

  linkChild(parentId: NodeId, key: Key, childId: NodeId): void {
    this.get(childId).inbound.push({ from: parentId, key, via: 'child' });
  }

  // Shallow copy with a fresh identity. Child edges are reused (shared),
  // and back-references from those children to the clone are registered.
  cloneShallow(src: GNode, opts: { stripId?: boolean } = {}): GNode {
    const clone = this.create(src.kind, opts.stripId ? undefined : src.idName);
    for (const [key, slot] of src.slots) {
      clone.slots.set(key, slot);
      if (slot.t === 'child') this.linkChild(clone.id, key, slot.node);
    }
    return clone;
  }

  // Deep private copy of a child-edge subgraph (copy-on-write materialization).
  // $id names are stripped: the copy has the same content but a new identity.
  // Ref edges inside are copied symbolically and still resolve to the originals.
  deepClonePrivate(rootId: NodeId): NodeId {
    const src = this.get(rootId);
    const clone = this.create(src.kind);
    for (const [key, slot] of src.slots) {
      if (slot.t === 'child') {
        const childCopy = this.deepClonePrivate(slot.node);
        clone.slots.set(key, { t: 'child', node: childCopy });
        this.linkChild(clone.id, key, childCopy);
      } else {
        clone.slots.set(key, slot);
      }
    }
    return clone.id;
  }

  allIds(): NodeId[] {
    return [...this.nodes.keys()];
  }

  remove(id: NodeId): void {
    this.nodes.delete(id);
  }

  get size(): number {
    return this.nodes.size;
  }
}
