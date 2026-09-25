import type { NodeId } from './types.ts';
import { TreeModel } from './tree.ts';

/**
 * Multi-selection, independent of focus. The range anchor is bound to the
 * tree version: once the tree mutates, a stale anchor is discarded and the
 * next range operation re-anchors at the current focus (deterministic).
 *
 * Disabled rules: a disabled node can never be toggled, anchored or focused;
 * range selection silently excludes disabled nodes inside the range.
 */
export class SelectionModel {
  readonly selected = new Set<NodeId>();
  private anchorId: NodeId | null = null;
  private anchorVersion = -1;

  private tree: TreeModel;

  constructor(tree: TreeModel) {
    this.tree = tree;
  }

  get anchor(): NodeId | null {
    return this.anchorId;
  }

  get anchorVersion(): number {
    return this.anchorVersion;
  }

  isAnchorStale(): boolean {
    return this.anchorId !== null && this.anchorVersion !== this.tree.version;
  }

  isSelected(id: NodeId): boolean {
    return this.selected.has(id);
  }

  /** Toggle a single node. Disabled/unknown nodes are ignored. */
  toggle(id: NodeId): boolean {
    if (!this.tree.exists(id) || this.tree.isDisabled(id)) return false;
    if (this.selected.has(id)) this.selected.delete(id);
    else this.selected.add(id);
    this.anchorId = id;
    this.anchorVersion = this.tree.version;
    return true;
  }

  /** Replace the selection with exactly `id`. */
  selectOnly(id: NodeId): boolean {
    if (!this.tree.exists(id) || this.tree.isDisabled(id)) return false;
    this.selected.clear();
    this.selected.add(id);
    this.anchorId = id;
    this.anchorVersion = this.tree.version;
    return true;
  }

  unselect(id: NodeId): void {
    this.selected.delete(id);
    if (this.anchorId === id) this.anchorId = null;
  }

  clear(): void {
    this.selected.clear();
    this.anchorId = null;
  }

  /**
   * Shift-range selection in visible order: the selection becomes exactly the
   * enabled nodes between the anchor and `focusId` (inclusive). A stale,
   * removed or disabled anchor first re-binds to `focusId`.
   */
  rangeTo(focusId: NodeId, visible: readonly NodeId[]): void {
    if (!this.tree.exists(focusId) || this.tree.isDisabled(focusId)) return;
    if (
      this.anchorId === null ||
      this.isAnchorStale() ||
      !this.tree.exists(this.anchorId) ||
      this.tree.isDisabled(this.anchorId)
    ) {
      this.anchorId = focusId;
    }
    this.anchorVersion = this.tree.version;
    const a = visible.indexOf(this.anchorId);
    const b = visible.indexOf(focusId);
    this.selected.clear();
    if (a < 0 || b < 0) {
      // Anchor or focus filtered out of view: fall back to focus only.
      this.selected.add(focusId);
      return;
    }
    const [lo, hi] = a <= b ? [a, b] : [b, a];
    for (let i = lo; i <= hi; i++) {
      const id = visible[i];
      if (!this.tree.isDisabled(id)) this.selected.add(id);
    }
  }

  /** Drop removed ids; invalidate the anchor if it was removed. */
  prune(removed: ReadonlySet<NodeId>): void {
    for (const id of removed) this.selected.delete(id);
    if (this.anchorId !== null && removed.has(this.anchorId)) this.anchorId = null;
  }
}
