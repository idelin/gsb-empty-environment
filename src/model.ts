/**
 * Core tree model.
 *
 * - Maintains an INCREMENTAL visible sequence: every node caches `vc`, the
 *   number of visible rows in its subtree (itself included). Structural
 *   changes (expand/collapse/insert/remove/move/load-apply) only touch the
 *   affected subtree and the ancestor chain — never a whole-tree traversal.
 *   A whole-tree recomputation happens only on construction and on
 *   `setFilter`, and is counted in `stats.fullRecomputes` so tests can prove
 *   the incremental paths stay incremental.
 * - `version` bumps on every mutation. Focus, selection range and type-ahead
 *   state (see view.ts) each record the version they were validated at.
 * - Async child loading is token-guarded: a stale or cancelled load result is
 *   dropped, and applying loaded children NEVER changes `expanded`, so an old
 *   load can never re-expand a branch the user already closed.
 */

export interface NodeData {
  id: string;
  label: string;
  disabled?: boolean;
  /** true => children are provided lazily through the loader */
  lazy?: boolean;
  children?: NodeData[];
}

export type FilterFn = (label: string, id: string) => boolean;

export interface RemovedInfo {
  id: string;
  parentId: string | null;
  /** index the node had in the visible sequence before removal, -1 if hidden */
  visibleIndex: number;
}

export class TreeNode {
  id: string;
  label: string;
  parent: TreeNode | null = null;
  children: TreeNode[] = [];
  disabled: boolean;
  expanded = false;
  lazy: boolean;
  loaded: boolean;
  loading = false;
  /** bumped to invalidate any in-flight load (stale-result / cancel guard) */
  loadToken = 0;
  detached = false;
  /** passes the current filter itself */
  matches = true;
  /** number of matching nodes among descendants */
  matchCount = 0;
  /** visible rows in this subtree, itself included (0 when self is hidden) */
  vc = 0;

  constructor(data: NodeData) {
    this.id = data.id;
    this.label = data.label;
    this.disabled = data.disabled === true;
    this.lazy = data.lazy === true;
    this.loaded = !this.lazy;
  }
}

export class TreeModel {
  root: TreeNode;
  version = 0;
  filterActive = false;
  private filterFn: FilterFn | null = null;
  private byId = new Map<string, TreeNode>();
  private loader: ((id: string) => Promise<NodeData[]>) | null;
  readonly stats = { fullRecomputes: 0 };

  constructor(data: NodeData, loader?: (id: string) => Promise<NodeData[]>) {
    this.loader = loader ?? null;
    this.root = this.build(data, null);
    this.root.expanded = true; // top-level rows are visible by default
    this.recomputeAll();
  }

  private build(data: NodeData, parent: TreeNode | null): TreeNode {
    const node = new TreeNode(data);
    node.parent = parent;
    this.byId.set(node.id, node);
    if (data.children) {
      for (const childData of data.children) {
        node.children.push(this.build(childData, node));
      }
    }
    return node;
  }

  private bump(): void {
    this.version++;
  }

  get(id: string): TreeNode | undefined {
    return this.byId.get(id);
  }

  has(id: string): boolean {
    return this.byId.has(id);
  }

  /** Whether the node itself is a visible row candidate (ignores ancestors). */
  isVisibleSelf(node: TreeNode): boolean {
    return !this.filterActive || node.matches || node.matchCount > 0;
  }

  /** Whether the node is actually part of the visible sequence. */
  isVisible(node: TreeNode): boolean {
    let current: TreeNode | null = node;
    while (current) {
      if (!this.isVisibleSelf(current)) return false;
      const parent: TreeNode | null = current.parent;
      if (parent && !parent.expanded) return false;
      current = parent;
    }
    return true;
  }

  /** Whole-tree recomputation. Only used by constructor and setFilter. */
  private recomputeAll(): void {
    this.stats.fullRecomputes++;
    const visit = (node: TreeNode): void => {
      node.matchCount = 0;
      for (const child of node.children) {
        visit(child);
        node.matchCount += child.matchCount + (child.matches ? 1 : 0);
      }
      node.matches = this.filterFn ? this.filterFn(node.label, node.id) : true;
    };
    // matches must be known before matchCount aggregation, so do two passes.
    const applyFilter = (node: TreeNode): void => {
      node.matches = this.filterFn ? this.filterFn(node.label, node.id) : true;
      for (const child of node.children) applyFilter(child);
    };
    applyFilter(this.root);
    visit(this.root);
    const computeVc = (node: TreeNode): number => {
      if (!this.isVisibleSelf(node)) {
        node.vc = 0;
        return 0;
      }
      let total = 1;
      if (node.expanded) {
        for (const child of node.children) total += computeVc(child);
      } else {
        for (const child of node.children) computeVc(child);
      }
      node.vc = total;
      return total;
    };
    computeVc(this.root);
  }

  /** Add `delta` to the vc of every ancestor that currently includes `from`. */
  private propagate(from: TreeNode, delta: number): void {
    let parent = from.parent;
    while (parent) {
      if (!parent.expanded || !this.isVisibleSelf(parent)) break;
      parent.vc += delta;
      parent = parent.parent;
    }
  }

  /** Visible-row delta contributed by the direct children of `node`. */
  private childrenDelta(node: TreeNode): number {
    let delta = 0;
    for (const child of node.children) delta += child.vc;
    return delta;
  }

  expand(id: string): void {
    const node = this.mustGet(id);
    if (node.expanded) return;
    node.expanded = true;
    if (this.isVisibleSelf(node)) {
      const delta = this.childrenDelta(node);
      node.vc += delta;
      this.propagate(node, delta);
    }
    this.bump();
    if (node.lazy && !node.loaded && !node.loading && this.loader) {
      this.startLoad(node);
    }
  }

  collapse(id: string): void {
    const node = this.mustGet(id);
    if (!node.expanded) return;
    node.expanded = false;
    if (this.isVisibleSelf(node)) {
      const delta = this.childrenDelta(node);
      node.vc -= delta;
      this.propagate(node, -delta);
    }
    this.bump();
  }

  toggle(id: string): void {
    const node = this.mustGet(id);
    if (node.expanded) this.collapse(id);
    else this.expand(id);
  }

  /** Insert a (possibly nested) subtree. Returns the new node's id. */
  insert(parentId: string, data: NodeData, index?: number): string {
    const parent = this.mustGet(parentId);
    this.insertNode(parent, data, index ?? parent.children.length);
    this.bump();
    return data.id;
  }

  /** Insert without bumping (used by load application, which bumps once). */
  private insertNode(parent: TreeNode, data: NodeData, index: number): TreeNode {
    const node = this.build(data, parent);
    // Freshly built nodes default to collapsed; compute vc for the new
    // subtree only (bounded by the subtree, never the whole tree).
    const computeVc = (n: TreeNode): number => {
      if (!this.isVisibleSelf(n)) {
        n.vc = 0;
        return 0;
      }
      let total = 1;
      if (n.expanded) {
        for (const child of n.children) total += computeVc(child);
      } else {
        for (const child of n.children) computeVc(child);
      }
      n.vc = total;
      return total;
    };
    computeVc(node);
    const at = Math.max(0, Math.min(index, parent.children.length));
    parent.children.splice(at, 0, node);
    if (parent.expanded && this.isVisibleSelf(parent)) {
      parent.vc += node.vc;
      this.propagate(parent, node.vc);
    }
    return node;
  }

  /** Remove a subtree. Returns info used by the view for focus fallback. */
  remove(id: string): RemovedInfo {
    const node = this.mustGet(id);
    if (node === this.root) throw new Error("cannot remove root");
    const parent = node.parent!;
    const info: RemovedInfo = {
      id,
      parentId: parent.id,
      visibleIndex: this.indexOf(node),
    };
    // Invalidate any in-flight load for nodes inside the removed subtree.
    const detach = (n: TreeNode): void => {
      n.detached = true;
      n.loadToken++;
      n.loading = false;
      this.byId.delete(n.id);
      for (const child of n.children) detach(child);
    };
    detach(node);
    parent.children.splice(parent.children.indexOf(node), 1);
    if (parent.expanded && this.isVisibleSelf(parent)) {
      parent.vc -= node.vc;
      this.propagate(parent, -node.vc);
    }
    this.bump();
    return info;
  }

  /** Move a subtree to a new parent (reorder). Ids survive, so focus and
   *  selection survive too; only the visible order changes. */
  move(id: string, newParentId: string, index?: number): void {
    const node = this.mustGet(id);
    const newParent = this.mustGet(newParentId);
    for (let p: TreeNode | null = newParent; p; p = p.parent) {
      if (p === node) throw new Error("cannot move a node into its own subtree");
    }
    const oldParent = node.parent!;
    if (oldParent.expanded && this.isVisibleSelf(oldParent)) {
      oldParent.vc -= node.vc;
      this.propagate(oldParent, -node.vc);
    }
    oldParent.children.splice(oldParent.children.indexOf(node), 1);
    node.parent = newParent;
    const at = Math.max(0, Math.min(index ?? newParent.children.length, newParent.children.length));
    newParent.children.splice(at, 0, node);
    if (newParent.expanded && this.isVisibleSelf(newParent)) {
      newParent.vc += node.vc;
      this.propagate(newParent, node.vc);
    }
    this.bump();
  }

  setDisabled(id: string, disabled: boolean): void {
    const node = this.mustGet(id);
    if (node.disabled === disabled) return;
    node.disabled = disabled;
    this.bump();
  }

  /** Setting a filter is the ONE operation allowed to recompute the whole
   *  tree; everything else stays incremental. */
  setFilter(fn: FilterFn | null): void {
    this.filterFn = fn;
    this.filterActive = fn !== null;
    this.recomputeAll();
    this.bump();
  }

  // ---- async loading -------------------------------------------------------

  private startLoad(node: TreeNode): void {
    const token = ++node.loadToken;
    node.loading = true;
    this.bump();
    this.loader!(node.id).then(
      (children) => {
        // Stale (superseded), cancelled, or detached result: drop it.
        if (node.detached || node.loadToken !== token) return;
        node.loading = false;
        node.loaded = true;
        for (const childData of children) {
          this.insertNode(node, childData, node.children.length);
        }
        // NOTE: `expanded` is never touched here. If the user collapsed the
        // branch while the load was in flight, it stays collapsed.
        this.bump();
      },
      () => {
        if (node.detached || node.loadToken !== token) return;
        node.loading = false;
        this.bump();
      },
    );
  }

  /** Cancel a pending load: the in-flight promise's result will be dropped. */
  cancelLoad(id: string): void {
    const node = this.mustGet(id);
    if (!node.loading) return;
    node.loadToken++;
    node.loading = false;
    this.bump();
  }

  /** Force a reload (supersedes any in-flight load). */
  reload(id: string): void {
    const node = this.mustGet(id);
    if (!this.loader || !node.lazy) return;
    node.loaded = false;
    this.startLoad(node);
  }

  // ---- visible sequence queries (all O(depth) or O(depth + count)) ---------

  get totalVisible(): number {
    return this.root.vc;
  }

  /** Visible index of a node, or -1 when it is not visible. O(depth). */
  indexOf(node: TreeNode): number {
    if (!this.isVisibleSelf(node)) return -1;
    let index = 0;
    let child = node;
    let parent = node.parent;
    while (parent) {
      if (!parent.expanded || !this.isVisibleSelf(parent)) return -1;
      index += 1; // the parent row itself
      for (const sibling of parent.children) {
        if (sibling === child) break;
        index += sibling.vc;
      }
      child = parent;
      parent = parent.parent;
    }
    return index;
  }

  /** Node at a visible index, or null when out of range. O(depth). */
  at(index: number): TreeNode | null {
    if (index < 0 || index >= this.root.vc) return null;
    let node = this.root;
    let rest = index;
    for (;;) {
      if (rest === 0) return node;
      rest -= 1;
      for (const child of node.children) {
        if (child.vc === 0) continue;
        if (rest < child.vc) {
          node = child;
          break;
        }
        rest -= child.vc;
      }
    }
  }

  /** A slice of the visible sequence, O(depth + count). */
  visibleSlice(start: number, count: number): TreeNode[] {
    const out: TreeNode[] = [];
    let node = this.at(start);
    while (node && out.length < count) {
      out.push(node);
      const next = this.indexOf(node) + 1;
      node = next < this.root.vc ? this.at(next) : null;
    }
    return out;
  }

  /** All visible ids in order (test/debug helper). */
  visibleIds(): string[] {
    return this.visibleSlice(0, this.root.vc).map((n) => n.id);
  }

  depth(node: TreeNode): number {
    let d = 0;
    let current = node.parent;
    while (current) {
      d++;
      current = current.parent;
    }
    return d;
  }

  private mustGet(id: string): TreeNode {
    const node = this.byId.get(id);
    if (!node) throw new Error(`unknown node: ${id}`);
    return node;
  }
}
