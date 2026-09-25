import type { NodeId, TreeEvent } from './types.ts';
import { TreeModel } from './tree.ts';

/**
 * Incrementally maintained flattening of the visible rows.
 *
 * Expand/collapse/insert/remove/move apply localized splices whose cost is
 * bounded by the affected subtree — the whole tree is never re-walked for
 * those updates. A full recomputation happens only when the filter changes
 * (or when metadata changes while a filter is active), because filtering is a
 * mode switch with non-local membership.
 *
 * Filter rule: with a non-empty filter, a node is visible iff its label
 * matches (case-insensitive substring) or it has a matching descendant;
 * expansion state is ignored while filtering.
 */
export class VisibleSequence {
  private list: NodeId[] = [];
  private pos = new Map<NodeId, number>();
  private posDirty = true;
  /** Test/diagnostic counters proving updates stay incremental. */
  recomputeCount = 0;
  spliceCount = 0;

  private tree: TreeModel;

  constructor(tree: TreeModel) {
    this.tree = tree;
    this.list = this.computeAll();
    tree.onChange((e) => this.apply(e));
  }

  get ids(): readonly NodeId[] {
    return this.list;
  }

  get length(): number {
    return this.list.length;
  }

  at(index: number): NodeId | undefined {
    return this.list[index];
  }

  includes(id: NodeId): boolean {
    return this.indexOf(id) >= 0;
  }

  indexOf(id: NodeId): number {
    if (this.posDirty) {
      this.pos.clear();
      for (let i = 0; i < this.list.length; i++) this.pos.set(this.list[i], i);
      this.posDirty = false;
    }
    return this.pos.get(id) ?? -1;
  }

  private splice(start: number, deleteCount: number, items: NodeId[]): void {
    this.list.splice(start, deleteCount, ...items);
    this.posDirty = true;
    this.spliceCount++;
  }

  /** Reference traversal: full walk of the (expanded) tree. */
  computeAll(): NodeId[] {
    const filter = this.tree.filterText.toLowerCase();
    const out: NodeId[] = [];
    if (filter === '') {
      const walk = (id: NodeId): void => {
        out.push(id);
        if (this.tree.isExpanded(id)) {
          for (const c of this.tree.childrenOf(id)) walk(c);
        }
      };
      for (const r of this.tree.childrenOf(null)) walk(r);
      return out;
    }
    const include = new Map<NodeId, boolean>();
    const evalNode = (id: NodeId): boolean => {
      let inc = this.tree.labelOf(id).toLowerCase().includes(filter);
      for (const c of this.tree.childrenOf(id)) {
        inc = evalNode(c) || inc;
      }
      include.set(id, inc);
      return inc;
    };
    for (const r of this.tree.childrenOf(null)) evalNode(r);
    const emit = (id: NodeId): void => {
      if (!include.get(id)) return;
      out.push(id);
      for (const c of this.tree.childrenOf(id)) emit(c);
    };
    for (const r of this.tree.childrenOf(null)) emit(r);
    return out;
  }

  private recompute(): void {
    this.list = this.computeAll();
    this.posDirty = true;
    this.recomputeCount++;
  }

  /** Nodes strictly below `id` that are visible given current expansion. */
  private visibleDescendants(id: NodeId): NodeId[] {
    const out: NodeId[] = [];
    const walk = (pid: NodeId): void => {
      for (const c of this.tree.childrenOf(pid)) {
        out.push(c);
        if (this.tree.isExpanded(c)) walk(c);
      }
    };
    walk(id);
    return out;
  }

  /** End index (exclusive) of the visible segment rooted at `id` in `list`. */
  private segmentEnd(id: NodeId): number {
    let j = this.indexOf(id) + 1;
    while (j < this.list.length && this.tree.isDescendantOf(this.list[j], id)) j++;
    return j;
  }

  private apply(e: TreeEvent): void {
    if (e.type === 'filter') {
      this.recompute();
      return;
    }
    if (this.tree.filterText !== '') {
      // Filtering is a non-local mode: any structural/meta change recomputes.
      this.recompute();
      return;
    }
    switch (e.type) {
      case 'meta':
        return; // label/disabled do not change membership without a filter
      case 'expand': {
        const i = this.indexOf(e.id);
        if (i < 0) return;
        this.splice(i + 1, 0, this.visibleDescendants(e.id));
        return;
      }
      case 'collapse': {
        const i = this.indexOf(e.id);
        if (i < 0) return;
        this.splice(i + 1, this.segmentEnd(e.id) - (i + 1), []);
        return;
      }
      case 'insert': {
        if (e.ids.length === 0) return;
        const at = this.insertionIndex(e.parentId, e.index);
        if (at === null) return; // parent chain hidden: nothing becomes visible
        const items: NodeId[] = [];
        for (const id of e.ids) {
          items.push(id, ...this.visibleDescendants(id));
        }
        this.splice(at, 0, items);
        return;
      }
      case 'remove': {
        const top = e.ids[0];
        const i = this.indexOf(top);
        if (i < 0) return;
        const gone = new Set(e.ids);
        let j = i;
        while (j < this.list.length && gone.has(this.list[j])) j++;
        this.splice(i, j - i, []);
        return;
      }
      case 'move': {
        const i = this.indexOf(e.id);
        let segment: NodeId[] = [];
        if (i >= 0) {
          const end = this.segmentEnd(e.id);
          segment = this.list.slice(i, end);
          this.splice(i, end - i, []);
        } else {
          segment = [e.id, ...this.visibleDescendants(e.id)];
        }
        const at = this.insertionIndex(e.toParent, e.index);
        if (at !== null) this.splice(at, 0, segment);
        return;
      }
    }
  }

  /**
   * Where the child at `index` of `parentId` starts in the visible list, or
   * null when that position is currently hidden (collapsed/absent ancestors).
   */
  private insertionIndex(parentId: NodeId | null, index: number): number | null {
    if (parentId === null) {
      if (index === 0) return 0;
      const prev = this.tree.childrenOf(null)[index - 1];
      return this.segmentEnd(prev);
    }
    const p = this.indexOf(parentId);
    if (p < 0 || !this.tree.isExpanded(parentId)) return null;
    if (index === 0) return p + 1;
    const prev = this.tree.childrenOf(parentId)[index - 1];
    return this.segmentEnd(prev);
  }
}
