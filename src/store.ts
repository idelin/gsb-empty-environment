import { formatPath, type JsonValue, type PathSeg } from "./json.ts";

export type NodeId = number;

/**
 * Every node in the input tree gets a stable, unique entity id.
 * Identity is by id, never by content: two nodes with equal JSON
 * values are still distinct entities.
 */
export interface DataNode {
  id: NodeId;
  kind: "object" | "array" | "value";
  parent: DataNode | null;
  key: PathSeg | null;
  value: JsonValue; // for kind === "value"
  children: DataNode[] | Map<string, DataNode> | null;
}

/** Tracks which entities a transaction dirtied. */
export interface ChangeSet {
  /** Nodes whose value/subtree was replaced or removed. */
  valueDirty: Set<NodeId>;
  /** Containers whose child structure (keys/order/length) changed. */
  structDirty: Set<NodeId>;
}

export function newChangeSet(): ChangeSet {
  return { valueDirty: new Set(), structDirty: new Set() };
}

export type Undo = () => void;

export class StoreError extends Error {}

export class Store {
  root: DataNode;
  readonly byId = new Map<NodeId, DataNode>();
  private nextId = 1;

  constructor(json: JsonValue) {
    this.root = this.build(json, null, null);
  }

  private build(json: JsonValue, parent: DataNode | null, key: PathSeg | null): DataNode {
    const node: DataNode = {
      id: this.nextId++,
      kind: "value",
      parent,
      key,
      value: null,
      children: null,
    };
    this.byId.set(node.id, node);
    if (json !== null && typeof json === "object") {
      if (Array.isArray(json)) {
        node.kind = "array";
        node.children = json.map((v, i) => this.build(v, node, i));
      } else {
        node.kind = "object";
        const m = new Map<string, DataNode>();
        for (const k of Object.keys(json)) m.set(k, this.build(json[k], node, k));
        node.children = m;
      }
    } else {
      node.value = json;
    }
    return node;
  }

  child(node: DataNode, seg: PathSeg): DataNode | undefined {
    if (node.kind === "array") {
      const idx = typeof seg === "number" ? seg : NaN;
      const arr = node.children as DataNode[];
      return Number.isInteger(idx) && idx >= 0 && idx < arr.length ? arr[idx] : undefined;
    }
    if (node.kind === "object") {
      return (node.children as Map<string, DataNode>).get(String(seg));
    }
    return undefined;
  }

  getNode(segs: PathSeg[]): DataNode | undefined {
    let cur = this.root;
    for (const s of segs) {
      const nxt = this.child(cur, s);
      if (!nxt) return undefined;
      cur = nxt;
    }
    return cur;
  }

  pathOf(node: DataNode): PathSeg[] {
    const segs: PathSeg[] = [];
    let cur: DataNode | null = node;
    while (cur && cur.parent) {
      segs.unshift(cur.key as PathSeg);
      cur = cur.parent;
    }
    return segs;
  }

  materialize(node: DataNode): JsonValue {
    if (node.kind === "value") return node.value;
    if (node.kind === "array") {
      return (node.children as DataNode[]).map((c) => this.materialize(c));
    }
    const out: { [k: string]: JsonValue } = {};
    for (const [k, c] of node.children as Map<string, DataNode>) {
      out[k] = this.materialize(c);
    }
    return out;
  }

  toJson(): JsonValue {
    return this.materialize(this.root);
  }

  private markSubtreeDirty(node: DataNode, cs: ChangeSet): void {
    cs.valueDirty.add(node.id);
    if (node.kind === "array") {
      for (const c of node.children as DataNode[]) this.markSubtreeDirty(c, cs);
    } else if (node.kind === "object") {
      for (const c of (node.children as Map<string, DataNode>).values()) {
        this.markSubtreeDirty(c, cs);
      }
    }
  }

  private reindex(arr: DataNode, from: number): void {
    const children = arr.children as DataNode[];
    for (let i = from; i < children.length; i++) children[i].key = i;
  }

  set(path: PathSeg[], json: JsonValue, cs: ChangeSet): Undo {
    if (path.length === 0) {
      const old = this.root;
      this.markSubtreeDirty(old, cs);
      this.root = this.build(json, null, null);
      return () => {
        this.root = old;
      };
    }
    const parent = this.getNode(path.slice(0, -1));
    if (!parent) throw new StoreError(`cannot set ${formatPath(path)}: parent missing`);
    const last = path[path.length - 1];
    if (parent.kind === "object") {
      const m = parent.children as Map<string, DataNode>;
      const key = String(last);
      const old = m.get(key);
      if (old) this.markSubtreeDirty(old, cs);
      else cs.structDirty.add(parent.id);
      const fresh = this.build(json, parent, key);
      m.set(key, fresh);
      return () => {
        if (old) m.set(key, old);
        else m.delete(key);
      };
    }
    if (parent.kind === "array") {
      const idx = typeof last === "number" ? last : NaN;
      const arr = parent.children as DataNode[];
      if (!Number.isInteger(idx) || idx < 0 || idx >= arr.length) {
        throw new StoreError(`cannot set ${formatPath(path)}: index out of range`);
      }
      const old = arr[idx];
      this.markSubtreeDirty(old, cs);
      arr[idx] = this.build(json, parent, idx);
      return () => {
        arr[idx] = old;
      };
    }
    throw new StoreError(`cannot set ${formatPath(path)}: parent is a scalar`);
  }

  insert(path: PathSeg[], index: number, json: JsonValue, cs: ChangeSet): Undo {
    const arr = this.getNode(path);
    if (!arr || arr.kind !== "array") {
      throw new StoreError(`cannot insert into ${formatPath(path)}: not an array`);
    }
    const children = arr.children as DataNode[];
    if (!Number.isInteger(index) || index < 0 || index > children.length) {
      throw new StoreError(`cannot insert into ${formatPath(path)}: bad index ${index}`);
    }
    cs.structDirty.add(arr.id);
    const node = this.build(json, arr, index);
    children.splice(index, 0, node);
    this.reindex(arr, index + 1);
    return () => {
      children.splice(index, 1);
      this.reindex(arr, index);
    };
  }

  remove(path: PathSeg[], cs: ChangeSet): Undo {
    if (path.length === 0) throw new StoreError("cannot remove root");
    const parent = this.getNode(path.slice(0, -1));
    if (!parent) throw new StoreError(`cannot remove ${formatPath(path)}: parent missing`);
    const last = path[path.length - 1];
    if (parent.kind === "object") {
      const m = parent.children as Map<string, DataNode>;
      const key = String(last);
      const old = m.get(key);
      if (!old) throw new StoreError(`cannot remove ${formatPath(path)}: key missing`);
      this.markSubtreeDirty(old, cs);
      cs.structDirty.add(parent.id);
      m.delete(key);
      return () => {
        m.set(key, old);
      };
    }
    if (parent.kind === "array") {
      const idx = typeof last === "number" ? last : NaN;
      const arr = parent.children as DataNode[];
      if (!Number.isInteger(idx) || idx < 0 || idx >= arr.length) {
        throw new StoreError(`cannot remove ${formatPath(path)}: index out of range`);
      }
      const old = arr[idx];
      this.markSubtreeDirty(old, cs);
      cs.structDirty.add(parent.id);
      arr.splice(idx, 1);
      this.reindex(parent, idx);
      return () => {
        arr.splice(idx, 0, old);
        this.reindex(parent, idx);
      };
    }
    throw new StoreError(`cannot remove ${formatPath(path)}: parent is a scalar`);
  }

  move(path: PathSeg[], from: number, to: number, cs: ChangeSet): Undo {
    const arr = this.getNode(path);
    if (!arr || arr.kind !== "array") {
      throw new StoreError(`cannot move in ${formatPath(path)}: not an array`);
    }
    const children = arr.children as DataNode[];
    if (
      !Number.isInteger(from) || !Number.isInteger(to) ||
      from < 0 || from >= children.length || to < 0 || to >= children.length
    ) {
      throw new StoreError(`cannot move in ${formatPath(path)}: bad index`);
    }
    cs.structDirty.add(arr.id);
    const [item] = children.splice(from, 1);
    children.splice(to, 0, item);
    this.reindex(arr, Math.min(from, to));
    return () => {
      const [back] = children.splice(to, 1);
      children.splice(from, 0, back);
      this.reindex(arr, Math.min(from, to));
    };
  }
}
