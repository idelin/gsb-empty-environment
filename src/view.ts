/**
 * View state on top of TreeModel: focus, selection and type-ahead.
 *
 * Disabled-node rules (single source of truth):
 *   D1. A disabled node can never be selected: plain/ctrl selection ignores
 *       it and range selection skips over it.
 *   D2. Keyboard navigation (up/down/home/end) and type-ahead never land on
 *       a disabled node; they skip to the next enabled visible row.
 *   D3. A disabled node may still be expanded/collapsed so its children stay
 *       reachable.
 *   D4. If a node becomes disabled (or hidden/removed) while focused, focus
 *       falls back deterministically: nearest enabled visible row at or after
 *       its last visible index, else the nearest one before, else null.
 *   D5. If a selected node becomes disabled or is removed, it is dropped from
 *       the selection on the next validation. Hidden-by-filter nodes that are
 *       still enabled KEEP their selection.
 *
 * Version binding: focus, the range anchor and the type-ahead buffer each
 * record the model version at which they were validated. Any structural
 * change bumps the version; the next read re-validates the corresponding
 * piece of state lazily and independently.
 */

import type { TreeModel, TreeNode } from "./model.ts";

export class TreeView {
  readonly model: TreeModel;

  private focusId: string | null = null;
  private focusVersion = -1;

  private selectedIds = new Set<string>();
  private selectionVersion = -1;

  private anchorId: string | null = null;
  private anchorVersion = -1;

  private typeBuffer = "";
  private typeVersion = -1;

  constructor(model: TreeModel) {
    this.model = model;
  }

  // ---- focus ---------------------------------------------------------------

  get focusVersionBound(): number {
    return this.focusVersion;
  }

  focus(): string | null {
    this.validateFocus();
    return this.focusId;
  }

  private validateFocus(): void {
    if (this.focusVersion === this.model.version) return;
    const node = this.focusId ? this.model.get(this.focusId) : undefined;
    if (node && this.model.isVisible(node) && !node.disabled) {
      this.focusVersion = this.model.version;
      return;
    }
    const startIndex = node ? Math.max(0, this.model.indexOf(node)) : 0;
    this.focusId = this.fallbackFromIndex(startIndex);
    this.focusVersion = this.model.version;
  }

  /** Deterministic fallback: first enabled visible row at/after `index`,
   *  else the nearest enabled visible row before it, else null. */
  private fallbackFromIndex(index: number): string | null {
    const total = this.model.totalVisible;
    if (total === 0) return null;
    for (let i = Math.min(index, total - 1); i < total; i++) {
      const node = this.model.at(i)!;
      if (!node.disabled) return node.id;
    }
    for (let i = Math.min(index, total - 1) - 1; i >= 0; i--) {
      const node = this.model.at(i)!;
      if (!node.disabled) return node.id;
    }
    return null;
  }

  setFocus(id: string): void {
    const node = this.model.get(id);
    if (!node || node.disabled || !this.model.isVisible(node)) return;
    this.focusId = id;
    this.focusVersion = this.model.version;
  }

  private stepFocus(direction: 1 | -1): void {
    this.validateFocus();
    const total = this.model.totalVisible;
    if (total === 0) return;
    const current = this.focusId ? this.model.get(this.focusId) : undefined;
    let index = current ? this.model.indexOf(current) : direction === 1 ? -1 : total;
    for (let i = index + direction; i >= 0 && i < total; i += direction) {
      const node = this.model.at(i)!;
      if (!node.disabled) {
        this.focusId = node.id;
        this.focusVersion = this.model.version;
        return;
      }
    }
  }

  moveDown(): void {
    this.stepFocus(1);
  }

  moveUp(): void {
    this.stepFocus(-1);
  }

  home(): void {
    this.validateFocus();
    for (let i = 0; i < this.model.totalVisible; i++) {
      const node = this.model.at(i)!;
      if (!node.disabled) {
        this.focusId = node.id;
        this.focusVersion = this.model.version;
        return;
      }
    }
  }

  end(): void {
    this.validateFocus();
    for (let i = this.model.totalVisible - 1; i >= 0; i--) {
      const node = this.model.at(i)!;
      if (!node.disabled) {
        this.focusId = node.id;
        this.focusVersion = this.model.version;
        return;
      }
    }
  }

  // ---- expand / collapse ---------------------------------------------------

  toggle(id?: string): void {
    const target = id ?? this.focus();
    if (!target) return;
    const node = this.model.get(target);
    if (!node) return;
    if (node.expanded) this.collapseNode(node);
    else this.model.expand(target);
  }

  /** Collapsing a subtree that contains the focus moves focus to the
   *  collapsed node itself (or its nearest enabled ancestor when disabled). */
  collapseNode(node: TreeNode): void {
    const focused = this.focusId ? this.model.get(this.focusId) : undefined;
    let containsFocus = false;
    for (let p = focused ? focused.parent : null; p; p = p.parent) {
      if (p === node) {
        containsFocus = true;
        break;
      }
    }
    this.model.collapse(node.id);
    if (containsFocus) {
      let target: TreeNode | null = node;
      while (target && target.disabled) target = target.parent;
      this.focusId = target ? target.id : null;
      this.focusVersion = this.model.version;
    }
  }

  // ---- selection -----------------------------------------------------------

  selection(): string[] {
    this.validateSelection();
    return [...this.selectedIds];
  }

  isSelected(id: string): boolean {
    this.validateSelection();
    return this.selectedIds.has(id);
  }

  private validateSelection(): void {
    if (this.selectionVersion === this.model.version) return;
    for (const id of [...this.selectedIds]) {
      const node = this.model.get(id);
      if (!node || node.disabled) this.selectedIds.delete(id);
    }
    this.selectionVersion = this.model.version;
  }

  anchor(): string | null {
    this.validateAnchor();
    return this.anchorId;
  }

  private validateAnchor(): void {
    if (this.anchorVersion === this.model.version) return;
    const node = this.anchorId ? this.model.get(this.anchorId) : undefined;
    if (!node || node.disabled || !this.model.isVisible(node)) {
      // Anchor fallback: the current focus when usable, else cleared.
      const focus = this.focusId ? this.model.get(this.focusId) : undefined;
      this.anchorId =
        focus && !focus.disabled && this.model.isVisible(focus) ? focus.id : null;
    }
    this.anchorVersion = this.model.version;
  }

  /** Plain click: select exactly this node (disabled nodes are ignored, D1). */
  select(id: string): void {
    const node = this.model.get(id);
    if (!node || node.disabled) return;
    this.selectedIds = new Set([id]);
    this.anchorId = id;
    this.anchorVersion = this.model.version;
    this.selectionVersion = this.model.version;
    this.focusId = id;
    this.focusVersion = this.model.version;
  }

  /** Ctrl/Cmd click: toggle one enabled node in the selection. */
  toggleSelect(id: string): void {
    const node = this.model.get(id);
    if (!node || node.disabled) return;
    this.validateSelection();
    if (this.selectedIds.has(id)) this.selectedIds.delete(id);
    else this.selectedIds.add(id);
    this.anchorId = id;
    this.anchorVersion = this.model.version;
    this.selectionVersion = this.model.version;
    this.focusId = id;
    this.focusVersion = this.model.version;
  }

  /** Shift click: select every ENABLED node between the anchor and `id`
   *  in visible order (D1 skips disabled rows inside the range). */
  selectRange(id: string): void {
    const node = this.model.get(id);
    if (!node || node.disabled || !this.model.isVisible(node)) return;
    this.validateAnchor();
    const anchorNode = this.anchorId ? this.model.get(this.anchorId) : undefined;
    const from = anchorNode && this.model.isVisible(anchorNode)
      ? this.model.indexOf(anchorNode)
      : this.model.indexOf(node);
    const to = this.model.indexOf(node);
    const lo = Math.min(from, to);
    const hi = Math.max(from, to);
    const next = new Set<string>();
    for (const nodeAt of this.model.visibleSlice(lo, hi - lo + 1)) {
      if (!nodeAt.disabled) next.add(nodeAt.id);
    }
    this.selectedIds = next;
    this.selectionVersion = this.model.version;
    this.focusId = id;
    this.focusVersion = this.model.version;
  }

  clearSelection(): void {
    this.selectedIds.clear();
    this.anchorId = null;
    this.anchorVersion = this.model.version;
    this.selectionVersion = this.model.version;
  }

  // ---- structural edits routed through the view ----------------------------

  /** Deterministic focus fallback on delete: nearest enabled visible row at
   *  or after the removed row's old index, else the nearest one before. */
  remove(id: string): void {
    const info = this.model.remove(id);
    if (this.focusId === id || (this.focusId && !this.model.has(this.focusId))) {
      this.focusId = this.fallbackFromIndex(info.visibleIndex < 0 ? 0 : info.visibleIndex);
      this.focusVersion = this.model.version;
    }
    if (this.anchorId && !this.model.has(this.anchorId)) {
      this.anchorId = this.focusId;
      this.anchorVersion = this.model.version;
    }
  }

  // ---- type-ahead ----------------------------------------------------------

  get typeAheadVersionBound(): number {
    return this.typeVersion;
  }

  get typeAheadBuffer(): string {
    return this.typeBuffer;
  }

  /** Type a character. Matches visible, enabled nodes by label prefix,
   *  starting after the current focus and wrapping. Repeating the same
   *  character cycles through matches. Matches are recomputed whenever the
   *  tree version moved on since the buffer was built. */
  type(char: string): void {
    const stale = this.typeVersion !== this.model.version;
    this.typeBuffer = stale ? char : this.typeBuffer + char;
    // A buffer made of one repeated character cycles through its matches.
    const repeated =
      this.typeBuffer.length > 1 &&
      this.typeBuffer.split("").every((c) => c === char);
    const query = (repeated ? char : this.typeBuffer).toLowerCase();
    const matches = this.collectTypeMatches(query);
    if (matches.length > 0) {
      const focused = this.focusId ? this.model.get(this.focusId) : undefined;
      const start = focused ? this.model.indexOf(focused) : -1;
      const next =
        matches.find((id) => this.model.indexOf(this.model.get(id)!) > start) ??
        matches[0];
      this.focusId = next;
      this.focusVersion = this.model.version;
    }
    this.typeVersion = this.model.version;
  }

  clearTypeAhead(): void {
    this.typeBuffer = "";
    this.typeVersion = this.model.version;
  }

  /** Matches in visible order; disabled rows never match (D2). */
  private collectTypeMatches(query: string): string[] {
    const total = this.model.totalVisible;
    if (total === 0 || query.length === 0) return [];
    const out: string[] = [];
    for (let i = 0; i < total; i++) {
      const node = this.model.at(i)!;
      if (node.disabled) continue;
      if (node.label.toLowerCase().startsWith(query)) out.push(node.id);
    }
    return out;
  }
}
