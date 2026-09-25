import type { NodeData, NodeId, TreeEvent } from './types.ts';

export interface NodeRecord {
  id: NodeId;
  parentId: NodeId | null;
  label: string;
  disabled: boolean;
  children: NodeId[];
  /** Lazy branch whose children have not been loaded yet. */
  hasChildren: boolean;
  loaded: boolean;
}

/**
 * Structural model of the tree. Owns the version counter: every mutation
 * (structure, expansion, filter, metadata) increments `version` exactly once
 * and is then broadcast as a fine-grained TreeEvent. Focus, the selection
 * anchor and the type-ahead buffer each record the version they were computed
 * against and are repaired/rebound when stale.
 */
export class TreeModel {
  private nodes = new Map<NodeId, NodeRecord>();
  private rootChildren: NodeId[] = [];
  private expanded = new Set<NodeId>();
  private loadTokens = new Map<NodeId, number>();
  private listeners = new Set<(event: TreeEvent) => void>();
  version = 0;
  filterText = '';

  constructor(data: NodeData[] = []) {
    for (const d of data) this.addRecord(null, d);
    this.rootChildren = data.map((d) => d.id);
  }

  onChange(fn: (event: TreeEvent) => void): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  private emit(event: TreeEvent): void {
    for (const fn of [...this.listeners]) fn(event);
  }

  private addRecord(parentId: NodeId | null, d: NodeData): void {
    if (this.nodes.has(d.id)) throw new Error(`duplicate node id: ${d.id}`);
    const rec: NodeRecord = {
      id: d.id,
      parentId,
      label: d.label,
      disabled: d.disabled === true,
      children: (d.children ?? []).map((c) => c.id),
      hasChildren: d.hasChildren === true,
      loaded: d.children !== undefined || d.hasChildren !== true,
    };
    this.nodes.set(d.id, rec);
    for (const c of d.children ?? []) this.addRecord(d.id, c);
  }

  // ---- queries ----------------------------------------------------------

  exists(id: NodeId): boolean {
    return this.nodes.has(id);
  }

  get(id: NodeId): NodeRecord | undefined {
    return this.nodes.get(id);
  }

  labelOf(id: NodeId): string {
    const rec = this.nodes.get(id);
    if (!rec) throw new Error(`unknown node: ${id}`);
    return rec.label;
  }

  parentOf(id: NodeId): NodeId | null {
    return this.nodes.get(id)?.parentId ?? null;
  }

  /** Children of `id`, or top-level order when `id` is null. */
  childrenOf(id: NodeId | null): readonly NodeId[] {
    if (id === null) return this.rootChildren;
    return this.nodes.get(id)?.children ?? [];
  }

  isDisabled(id: NodeId): boolean {
    return this.nodes.get(id)?.disabled ?? false;
  }

  isExpanded(id: NodeId): boolean {
    return this.expanded.has(id);
  }

  isLoaded(id: NodeId): boolean {
    return this.nodes.get(id)?.loaded ?? false;
  }

  hasLazyChildren(id: NodeId): boolean {
    const rec = this.nodes.get(id);
    return rec !== undefined && rec.hasChildren && !rec.loaded;
  }

  isLoadPending(id: NodeId): boolean {
    return this.loadTokens.has(id);
  }

  depthOf(id: NodeId): number {
    let depth = 0;
    let cur = this.parentOf(id);
    while (cur !== null) {
      depth++;
      cur = this.parentOf(cur);
    }
    return depth;
  }

  /** Strict descendant test. */
  isDescendantOf(id: NodeId, ancestorId: NodeId): boolean {
    let cur = this.parentOf(id);
    while (cur !== null) {
      if (cur === ancestorId) return true;
      cur = this.parentOf(cur);
    }
    return false;
  }

  /** Preorder ids of the subtree rooted at `id` (including `id`). */
  subtreeIds(id: NodeId): NodeId[] {
    const out: NodeId[] = [];
    const walk = (x: NodeId): void => {
      out.push(x);
      for (const c of this.childrenOf(x)) walk(c);
    };
    walk(id);
    return out;
  }

  expandedIds(): NodeId[] {
    return [...this.expanded];
  }

  allIds(): NodeId[] {
    const out: NodeId[] = [];
    const walk = (x: NodeId): void => {
      out.push(x);
      for (const c of this.childrenOf(x)) walk(c);
    };
    for (const r of this.rootChildren) walk(r);
    return out;
  }

  // ---- expansion ----------------------------------------------------------

  expand(id: NodeId): boolean {
    const rec = this.nodes.get(id);
    if (!rec || this.expanded.has(id)) return false;
    if (rec.children.length === 0 && !rec.hasChildren) return false;
    this.expanded.add(id);
    this.version++;
    this.emit({ type: 'expand', id });
    return true;
  }

  collapse(id: NodeId): boolean {
    if (!this.expanded.delete(id)) return false;
    this.version++;
    this.emit({ type: 'collapse', id });
    return true;
  }

  // ---- filter ---------------------------------------------------------------

  setFilter(text: string): boolean {
    const next = text.trim();
    if (next === this.filterText) return false;
    this.filterText = next;
    this.version++;
    this.emit({ type: 'filter' });
    return true;
  }

  // ---- structure --------------------------------------------------------------

  insertChildren(parentId: NodeId | null, nodes: NodeData[], index?: number): NodeId[] {
    if (parentId !== null && !this.nodes.has(parentId)) {
      throw new Error(`unknown parent: ${parentId}`);
    }
    const ids: NodeId[] = [];
    for (const d of nodes) {
      this.addRecord(parentId, d);
      ids.push(d.id);
    }
    const siblings = parentId === null ? this.rootChildren : this.nodes.get(parentId)!.children;
    const at = index === undefined ? siblings.length : Math.max(0, Math.min(index, siblings.length));
    siblings.splice(at, 0, ...ids);
    this.version++;
    this.emit({ type: 'insert', parentId, ids, index: at });
    return ids;
  }

  remove(id: NodeId): NodeId[] {
    const rec = this.nodes.get(id);
    if (!rec) return [];
    const ids = this.subtreeIds(id);
    const siblings = rec.parentId === null ? this.rootChildren : this.nodes.get(rec.parentId)!.children;
    const i = siblings.indexOf(id);
    if (i >= 0) siblings.splice(i, 1);
    for (const x of ids) {
      this.nodes.delete(x);
      this.expanded.delete(x);
      this.loadTokens.delete(x);
    }
    this.version++;
    this.emit({ type: 'remove', ids });
    return ids;
  }

  /** Reorder/reparent. Refuses cycles (moving a node into its own subtree). */
  move(id: NodeId, newParentId: NodeId | null, index: number): boolean {
    const rec = this.nodes.get(id);
    if (!rec) return false;
    if (newParentId !== null) {
      if (!this.nodes.has(newParentId)) return false;
      if (newParentId === id || this.isDescendantOf(newParentId, id)) return false;
    }
    const from = rec.parentId === null ? this.rootChildren : this.nodes.get(rec.parentId)!.children;
    const fi = from.indexOf(id);
    if (fi < 0) return false;
    from.splice(fi, 1);
    const to = newParentId === null ? this.rootChildren : this.nodes.get(newParentId)!.children;
    const at = Math.max(0, Math.min(index, to.length));
    to.splice(at, 0, id);
    rec.parentId = newParentId;
    this.version++;
    this.emit({ type: 'move', id, toParent: newParentId, index: at });
    return true;
  }

  setLabel(id: NodeId, label: string): boolean {
    const rec = this.nodes.get(id);
    if (!rec || rec.label === label) return false;
    rec.label = label;
    this.version++;
    this.emit({ type: 'meta', id });
    return true;
  }

  setDisabled(id: NodeId, disabled: boolean): boolean {
    const rec = this.nodes.get(id);
    if (!rec || rec.disabled === disabled) return false;
    rec.disabled = disabled;
    this.version++;
    this.emit({ type: 'meta', id });
    return true;
  }

  // ---- async loading ------------------------------------------------------------
  // A monotonically increasing token per node invalidates stale loads: only the
  // most recently begun load may complete. Completing a load inserts children
  // but NEVER touches expansion state, so a stale load cannot re-expand a
  // branch the user has closed in the meantime.

  beginLoad(id: NodeId): number {
    const token = (this.loadTokens.get(id) ?? 0) + 1;
    this.loadTokens.set(id, token);
    return token;
  }

  cancelLoad(id: NodeId): void {
    if (this.loadTokens.has(id)) this.beginLoad(id);
  }

  isLoadCurrent(id: NodeId, token: number): boolean {
    return this.loadTokens.get(id) === token && this.nodes.has(id);
  }

  completeLoad(id: NodeId, token: number, children: NodeData[]): NodeId[] | null {
    if (!this.isLoadCurrent(id, token)) return null; // stale or cancelled: discard
    this.loadTokens.delete(id);
    const rec = this.nodes.get(id)!;
    const ids: NodeId[] = [];
    for (const d of children) {
      this.addRecord(id, d);
      ids.push(d.id);
    }
    rec.children.push(...ids);
    rec.loaded = true;
    rec.hasChildren = false;
    this.version++;
    this.emit({ type: 'insert', parentId: id, ids, index: rec.children.length - ids.length });
    return ids;
  }

  failLoad(id: NodeId, token: number): boolean {
    if (!this.isLoadCurrent(id, token)) return false;
    this.loadTokens.delete(id);
    this.version++;
    this.emit({ type: 'meta', id });
    return true;
  }
}
