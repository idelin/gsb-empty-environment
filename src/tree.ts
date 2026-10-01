import {
  center,
  containsPoint,
  intersects,
  scrollDeltaToReveal,
  translate,
  type Rect,
} from './geometry.ts';

export type NodeId = string;

export type MountState = 'mounted' | 'unmounted' | 'mounting';

export interface FocusNodeInit {
  id: NodeId;
  parentId?: NodeId | null;
  /** Rect in absolute document coordinates (scroll offsets are applied on top). */
  rect: Rect;
  focusable?: boolean;
  modal?: boolean;
  scrollable?: boolean;
  collapsed?: boolean;
  mountState?: MountState;
  occludedBy?: NodeId[];
  label?: string;
}

export interface FocusNode {
  id: NodeId;
  parentId: NodeId | null;
  rect: Rect;
  focusable: boolean;
  modal: boolean;
  scrollable: boolean;
  collapsed: boolean;
  mountState: MountState;
  occludedBy: NodeId[];
  label: string;
  children: NodeId[];
  scrollOffset: { x: number; y: number };
}

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T | PromiseLike<T>) => void;
  reject: (reason?: unknown) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/**
 * Layout tree computed entirely from fixture data: rects, mount states,
 * occlusion, collapse and scroll offsets. Every mutation bumps
 * `layoutVersion`, which the focus controller validates after async work.
 */
export class LayoutTree {
  readonly rootId: NodeId = 'root';
  layoutVersion = 0;

  private nodes = new Map<NodeId, FocusNode>();
  private pendingMounts = new Map<NodeId, Deferred<void>>();
  private pendingScrolls = new Map<NodeId, { offset: { x: number; y: number }; d: Deferred<void> }>();

  constructor() {
    this.addNode({ id: this.rootId, rect: { x: 0, y: 0, w: 0, h: 0 } });
  }

  get size(): number {
    return this.nodes.size;
  }

  private bump(): void {
    this.layoutVersion += 1;
  }

  addNode(init: FocusNodeInit): FocusNode {
    if (this.nodes.has(init.id)) throw new Error(`duplicate node ${init.id}`);
    const isRoot = init.id === this.rootId && !this.nodes.has(this.rootId);
    const parentId = init.parentId ?? (isRoot ? null : this.rootId);
    const parent = parentId === null ? null : this.nodes.get(parentId);
    if (parentId !== null && !parent) throw new Error(`unknown parent ${parentId}`);
    const node: FocusNode = {
      id: init.id,
      parentId,
      rect: { ...init.rect },
      focusable: init.focusable ?? false,
      modal: init.modal ?? false,
      scrollable: init.scrollable ?? false,
      collapsed: init.collapsed ?? false,
      mountState: init.mountState ?? 'mounted',
      occludedBy: [...(init.occludedBy ?? [])],
      label: init.label ?? init.id,
      children: [],
      scrollOffset: { x: 0, y: 0 },
    };
    this.nodes.set(node.id, node);
    parent?.children.push(node.id);
    this.bump();
    return node;
  }

  has(id: NodeId): boolean {
    return this.nodes.has(id);
  }

  get(id: NodeId): FocusNode {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`unknown node ${id}`);
    return node;
  }

  parentOf(id: NodeId): NodeId | null {
    return this.get(id).parentId;
  }

  /** Pre-order DFS ids — this is the logical (reading) order. */
  subtreeIds(id: NodeId): NodeId[] {
    const out: NodeId[] = [];
    const walk = (nid: NodeId): void => {
      const node = this.nodes.get(nid);
      if (!node) return;
      out.push(nid);
      for (const child of node.children) walk(child);
    };
    walk(id);
    return out;
  }

  logicalOrder(): NodeId[] {
    return this.subtreeIds(this.rootId);
  }

  logicalIndex(id: NodeId): number {
    return this.logicalOrder().indexOf(id);
  }

  /** Rect with all ancestor scroll offsets applied (viewport coordinates). */
  effectiveRect(id: NodeId): Rect {
    let rect = { ...this.get(id).rect };
    let cursor = this.get(id).parentId;
    while (cursor) {
      const ancestor = this.get(cursor);
      if (ancestor.scrollable) {
        rect = translate(rect, -ancestor.scrollOffset.x, -ancestor.scrollOffset.y);
      }
      cursor = ancestor.parentId;
    }
    return rect;
  }

  private ancestorsOf(id: NodeId): FocusNode[] {
    const out: FocusNode[] = [];
    let cursor = this.get(id).parentId;
    while (cursor) {
      const node = this.get(cursor);
      out.push(node);
      cursor = node.parentId;
    }
    return out;
  }

  private isMountedChain(id: NodeId): boolean {
    if (this.get(id).mountState !== 'mounted') return false;
    return this.ancestorsOf(id).every((a) => a.mountState === 'mounted');
  }

  private hasCollapsedChain(id: NodeId): boolean {
    if (this.get(id).collapsed) return true;
    return this.ancestorsOf(id).some((a) => a.collapsed);
  }

  /** Purely geometric: does the effective rect intersect every scrollable ancestor viewport? */
  clipOk(id: NodeId): boolean {
    const rect = this.effectiveRect(id);
    for (const ancestor of this.ancestorsOf(id)) {
      if (!ancestor.scrollable) continue;
      if (!intersects(rect, this.effectiveRect(ancestor.id))) return false;
    }
    return true;
  }

  private isOccluded(id: NodeId): boolean {
    const node = this.get(id);
    if (!node.occludedBy.length) return false;
    const c = center(this.effectiveRect(id));
    return node.occludedBy.some((occId) => {
      if (!this.nodes.has(occId)) return false;
      if (!this.isMountedChain(occId) || this.hasCollapsedChain(occId)) return false;
      return containsPoint(this.effectiveRect(occId), c);
    });
  }

  isVisible(id: NodeId): boolean {
    if (!this.nodes.has(id)) return false;
    return (
      this.isMountedChain(id) &&
      !this.hasCollapsedChain(id) &&
      this.clipOk(id) &&
      !this.isOccluded(id)
    );
  }

  /** Fully focusable right now: exists, focusable and visible. */
  isFocusableNow(id: NodeId): boolean {
    return this.nodes.has(id) && this.get(id).focusable && this.isVisible(id);
  }

  /**
   * Plan-time eligibility: the node is not currently reachable, but could
   * become so via mount and/or scroll. Collapsed subtrees and occluded
   * nodes can never become visible through those actions, so they are out.
   */
  isEligible(id: NodeId): boolean {
    if (!this.nodes.has(id)) return false;
    const node = this.get(id);
    if (!node.focusable) return false;
    if (this.hasCollapsedChain(id)) return false;
    if (this.isOccluded(id)) return false;
    return true;
  }

  /** Ids on the path from `id` to the root whose mount state is not 'mounted'. */
  unmountedChain(id: NodeId): NodeId[] {
    const out: NodeId[] = [];
    if (this.get(id).mountState !== 'mounted') out.push(id);
    for (const ancestor of this.ancestorsOf(id)) {
      if (ancestor.mountState !== 'mounted') out.push(ancestor.id);
    }
    return out;
  }

  /** Would the node be outside any scrollable ancestor viewport (geometry only)? */
  needsScroll(id: NodeId): boolean {
    return !this.clipOk(id);
  }

  /** Nearest enclosing modal (inclusive), or the root when there is none. */
  modalScopeOf(id: NodeId): NodeId {
    let cursor: NodeId | null = id;
    while (cursor) {
      const node = this.get(cursor);
      if (node.modal) return node.id;
      cursor = node.parentId;
    }
    return this.rootId;
  }

  // --- async operations (deferred, completed by fixtures / replay steps) ---

  requestMount(id: NodeId): Promise<void> {
    const node = this.nodes.get(id);
    if (!node || node.mountState === 'mounted') return Promise.resolve();
    const existing = this.pendingMounts.get(id);
    if (existing) return existing.promise;
    node.mountState = 'mounting';
    const d = deferred<void>();
    this.pendingMounts.set(id, d);
    return d.promise;
  }

  completeMount(id: NodeId): void {
    const node = this.nodes.get(id);
    if (node) node.mountState = 'mounted';
    const d = this.pendingMounts.get(id);
    this.pendingMounts.delete(id);
    this.bump();
    d?.resolve();
  }

  /** Synchronous mount helper for fixtures/tests. */
  mountNow(id: NodeId): void {
    this.completeMount(id);
  }

  requestScrollTo(targetId: NodeId): Promise<void> {
    const rect = this.effectiveRect(targetId);
    const waits: Promise<void>[] = [];
    for (const ancestor of this.ancestorsOf(targetId)) {
      if (!ancestor.scrollable) continue;
      const viewport = this.effectiveRect(ancestor.id);
      const delta = scrollDeltaToReveal(viewport, rect);
      if (Math.abs(delta.x) < 1e-9 && Math.abs(delta.y) < 1e-9) continue;
      const offset = {
        x: ancestor.scrollOffset.x + delta.x,
        y: ancestor.scrollOffset.y + delta.y,
      };
      const existing = this.pendingScrolls.get(ancestor.id);
      if (existing) {
        existing.offset = offset;
        waits.push(existing.d.promise);
      } else {
        const d = deferred<void>();
        this.pendingScrolls.set(ancestor.id, { offset, d });
        waits.push(d.promise);
      }
    }
    return Promise.all(waits).then(() => undefined);
  }

  completeScroll(containerId: NodeId): void {
    const pending = this.pendingScrolls.get(containerId);
    if (!pending) return;
    const node = this.nodes.get(containerId);
    if (node) node.scrollOffset = { ...pending.offset };
    this.pendingScrolls.delete(containerId);
    this.bump();
    pending.d.resolve();
  }

  hasPending(): boolean {
    return this.pendingMounts.size > 0 || this.pendingScrolls.size > 0;
  }

  /** Complete every pending mount/scroll, iterating until quiescent. */
  async completeAllPending(maxRounds = 64): Promise<void> {
    for (let round = 0; round < maxRounds; round += 1) {
      if (!this.hasPending()) return;
      for (const id of [...this.pendingMounts.keys()]) this.completeMount(id);
      for (const id of [...this.pendingScrolls.keys()]) this.completeScroll(id);
      await new Promise((resolve) => setImmediate(resolve));
    }
    throw new Error('completeAllPending: tree did not settle');
  }

  // --- mutations ---

  delete(id: NodeId): void {
    const node = this.nodes.get(id);
    if (!node || id === this.rootId) return;
    for (const sub of this.subtreeIds(id)) {
      this.nodes.delete(sub);
      const pm = this.pendingMounts.get(sub);
      if (pm) {
        this.pendingMounts.delete(sub);
        pm.resolve();
      }
      const ps = this.pendingScrolls.get(sub);
      if (ps) {
        this.pendingScrolls.delete(sub);
        ps.d.resolve();
      }
    }
    if (node.parentId && this.nodes.has(node.parentId)) {
      const siblings = this.get(node.parentId).children;
      const idx = siblings.indexOf(id);
      if (idx >= 0) siblings.splice(idx, 1);
    }
    this.bump();
  }

  setCollapsed(id: NodeId, collapsed: boolean): void {
    this.get(id).collapsed = collapsed;
    this.bump();
  }

  setRect(id: NodeId, rect: Rect): void {
    this.get(id).rect = { ...rect };
    this.bump();
  }
}
