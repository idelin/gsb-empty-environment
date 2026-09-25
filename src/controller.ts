import type { GlyphMetrics, NodeData, NodeId } from './types.ts';
import { TreeModel } from './tree.ts';
import { VisibleSequence } from './visible.ts';
import { FocusModel } from './focus.ts';
import { SelectionModel } from './selection.ts';
import { TypeAhead } from './typeahead.ts';
import { contentWidth, rowGeometry, type RowGeometry } from './measure.ts';

export interface Row {
  id: NodeId;
  label: string;
  depth: number;
  disabled: boolean;
  expanded: boolean;
  hasChildren: boolean;
  loaded: boolean;
  focused: boolean;
  selected: boolean;
}

export interface ControllerOptions {
  now?: () => number;
  typeaheadTimeout?: number;
  /** Default loader used when expanding a lazy branch. */
  loader?: (id: NodeId) => Promise<NodeData[]>;
}

/**
 * Wires the models together and defines the deterministic policies:
 *
 * - Focus and selection are separate; keyboard moves focus, Shift extends
 *   the selection in visible order, Ctrl/Space toggles single nodes.
 * - Disabled nodes: never focusable (skipped by navigation and type-ahead),
 *   never selectable, excluded from ranges; disabling a focused/selected
 *   node moves focus to the nearest enabled visible row and drops it from
 *   the selection.
 * - Collapsing the subtree that contains the focus moves focus to the
 *   collapsed node (or its nearest enabled ancestor).
 * - Deleting the focused node falls back deterministically: first enabled
 *   visible row after the removed segment, else the last enabled row before
 *   it, else null.
 * - Async loads never change expansion and never touch the selection, so a
 *   stale load cannot re-expand a closed branch nor reset the selection.
 */
export class TreeController {
  readonly tree: TreeModel;
  readonly visible: VisibleSequence;
  readonly focus: FocusModel;
  readonly selection: SelectionModel;
  readonly typeahead: TypeAhead;
  private loader?: (id: NodeId) => Promise<NodeData[]>;

  constructor(data: NodeData[] = [], opts: ControllerOptions = {}) {
    this.tree = new TreeModel(data);
    this.visible = new VisibleSequence(this.tree);
    this.focus = new FocusModel(this.tree);
    this.selection = new SelectionModel(this.tree);
    this.typeahead = new TypeAhead(
      this.tree,
      opts.typeaheadTimeout ?? 800,
      opts.now ?? Date.now,
    );
    this.loader = opts.loader;
  }

  // ---- queries ------------------------------------------------------------

  rows(): Row[] {
    return this.visible.ids.map((id) => ({
      id,
      label: this.tree.labelOf(id),
      depth: this.tree.depthOf(id),
      disabled: this.tree.isDisabled(id),
      expanded: this.tree.isExpanded(id),
      hasChildren: this.tree.get(id)!.hasChildren,
      loaded: this.tree.isLoaded(id),
      focused: this.focus.id === id,
      selected: this.selection.isSelected(id),
    }));
  }

  geometry(metrics: GlyphMetrics, indentPx = 16): RowGeometry[] {
    return this.rows().map((r) => rowGeometry(r.id, r.depth, r.label, metrics, indentPx));
  }

  contentWidth(metrics: GlyphMetrics, indentPx = 16): number {
    return contentWidth(this.geometry(metrics, indentPx));
  }

  /** Repair invariants if any model is stale w.r.t. the tree version. */
  sync(): void {
    const f = this.focus.id;
    if (f !== null && (!this.tree.exists(f) || this.tree.isDisabled(f) || !this.visible.includes(f))) {
      this.focus.set(this.firstEnabledVisible(), this.tree.version);
    } else if (this.focus.isStale()) {
      this.focus.touch(this.tree.version);
    }
    for (const id of [...this.selection.selected]) {
      if (!this.tree.exists(id) || this.tree.isDisabled(id)) this.selection.unselect(id);
    }
  }

  private firstEnabledVisible(): NodeId | null {
    for (const id of this.visible.ids) {
      if (!this.tree.isDisabled(id)) return id;
    }
    return null;
  }

  // ---- focus ---------------------------------------------------------------

  private setFocus(id: NodeId | null, extend: boolean): void {
    if (id !== null && this.tree.isDisabled(id)) return;
    this.focus.set(id, this.tree.version);
    if (extend && id !== null) this.selection.rangeTo(id, this.visible.ids);
  }

  moveDown(extend = false): void {
    const list = this.visible.ids;
    const cur = this.focus.id === null ? -1 : list.indexOf(this.focus.id);
    for (let i = cur + 1; i < list.length; i++) {
      if (!this.tree.isDisabled(list[i])) {
        this.setFocus(list[i], extend);
        return;
      }
    }
  }

  moveUp(extend = false): void {
    const list = this.visible.ids;
    const cur = this.focus.id === null ? list.length : list.indexOf(this.focus.id);
    for (let i = cur - 1; i >= 0; i--) {
      if (!this.tree.isDisabled(list[i])) {
        this.setFocus(list[i], extend);
        return;
      }
    }
  }

  home(extend = false): void {
    this.setFocus(this.firstEnabledVisible(), extend);
  }

  end(extend = false): void {
    const list = this.visible.ids;
    for (let i = list.length - 1; i >= 0; i--) {
      if (!this.tree.isDisabled(list[i])) {
        this.setFocus(list[i], extend);
        return;
      }
    }
  }

  // ---- expansion -------------------------------------------------------------

  expand(id: NodeId): boolean {
    const ok = this.tree.expand(id);
    if (ok && this.loader && this.tree.hasLazyChildren(id) && !this.tree.isLoadPending(id)) {
      void this.loadChildren(id);
    }
    return ok;
  }

  collapse(id: NodeId): boolean {
    const f = this.focus.id;
    const contained = f !== null && f !== id && this.tree.isDescendantOf(f, id);
    const ok = this.tree.collapse(id);
    if (ok && contained) {
      // Focus falls to the collapsed node, or its nearest enabled ancestor.
      let target: NodeId | null = id;
      while (target !== null && this.tree.isDisabled(target)) {
        target = this.tree.parentOf(target);
      }
      this.focus.set(target, this.tree.version);
    }
    return ok;
  }

  toggleExpanded(id: NodeId): void {
    if (this.tree.isExpanded(id)) this.collapse(id);
    else this.expand(id);
  }

  // ---- selection ---------------------------------------------------------------

  toggleSelect(id: NodeId): boolean {
    return this.selection.toggle(id);
  }

  /** Click semantics: plain = select only, ctrl = toggle, shift = range. */
  click(id: NodeId, mods: { ctrl?: boolean; shift?: boolean } = {}): void {
    if (!this.tree.exists(id) || this.tree.isDisabled(id)) return;
    if (mods.shift) {
      this.focus.set(id, this.tree.version);
      this.selection.rangeTo(id, this.visible.ids);
    } else if (mods.ctrl) {
      this.selection.toggle(id);
      this.focus.set(id, this.tree.version);
    } else {
      this.selection.selectOnly(id);
      this.focus.set(id, this.tree.version);
    }
  }

  // ---- type-ahead ---------------------------------------------------------------

  typeChar(ch: string): NodeId | null {
    const hit = this.typeahead.input(ch, this.visible.ids, this.focus.id);
    if (hit !== null) this.focus.set(hit, this.tree.version);
    return hit;
  }

  escape(): void {
    this.typeahead.cancel();
  }

  // ---- keyboard -----------------------------------------------------------------

  handleKey(key: string, mods: { shift?: boolean } = {}): void {
    const shift = mods.shift === true;
    switch (key) {
      case 'ArrowDown':
        this.moveDown(shift);
        return;
      case 'ArrowUp':
        this.moveUp(shift);
        return;
      case 'Home':
        this.home(shift);
        return;
      case 'End':
        this.end(shift);
        return;
      case 'ArrowRight': {
        const f = this.focus.id;
        if (f !== null && !this.tree.isExpanded(f)) this.expand(f);
        return;
      }
      case 'ArrowLeft': {
        const f = this.focus.id;
        if (f === null) return;
        if (this.tree.isExpanded(f)) {
          this.collapse(f);
        } else {
          let p = this.tree.parentOf(f);
          while (p !== null && this.tree.isDisabled(p)) p = this.tree.parentOf(p);
          if (p !== null) this.focus.set(p, this.tree.version);
        }
        return;
      }
      case ' ':
      case 'Enter': {
        const f = this.focus.id;
        if (f !== null) this.selection.toggle(f);
        return;
      }
      case 'Escape':
        this.escape();
        return;
      default:
        if (key.length === 1) this.typeChar(key);
    }
  }

  // ---- structure ------------------------------------------------------------------

  insertChildren(parentId: NodeId | null, nodes: NodeData[], index?: number): NodeId[] {
    // Insertion (including async loads) never touches focus or selection.
    return this.tree.insertChildren(parentId, nodes, index);
  }

  removeNode(id: NodeId): NodeId[] {
    const removed = new Set(this.tree.subtreeIds(id));
    let fallback: NodeId | null = null;
    let needFallback = false;
    if (this.focus.id !== null && removed.has(this.focus.id)) {
      needFallback = true;
      const list = [...this.visible.ids];
      const top = list.indexOf(id);
      let j = top;
      while (j < list.length && removed.has(list[j])) j++;
      for (let k = j; k < list.length; k++) {
        if (!this.tree.isDisabled(list[k])) {
          fallback = list[k];
          break;
        }
      }
      if (fallback === null) {
        for (let k = top - 1; k >= 0; k--) {
          if (!removed.has(list[k]) && !this.tree.isDisabled(list[k])) {
            fallback = list[k];
            break;
          }
        }
      }
    }
    const ids = this.tree.remove(id);
    this.selection.prune(new Set(ids));
    if (needFallback) this.focus.set(fallback, this.tree.version);
    return ids;
  }

  moveNode(id: NodeId, newParent: NodeId | null, index: number): boolean {
    const ok = this.tree.move(id, newParent, index);
    if (ok) this.repairFocusVisibility();
    return ok;
  }

  setFilter(text: string): void {
    if (this.tree.setFilter(text)) this.repairFocusVisibility();
  }

  /**
   * Focus must always rest on a visible, enabled row. When a mutation hides
   * the focused row (reparented under a collapsed branch, filtered out), move
   * to the nearest enabled visible ancestor, else the first enabled row.
   */
  private repairFocusVisibility(): void {
    const f = this.focus.id;
    if (f === null) return;
    if (this.tree.exists(f) && !this.tree.isDisabled(f) && this.visible.includes(f)) {
      this.focus.touch(this.tree.version);
      return;
    }
    let p = this.tree.exists(f) ? this.tree.parentOf(f) : null;
    while (p !== null) {
      if (this.visible.includes(p) && !this.tree.isDisabled(p)) {
        this.focus.set(p, this.tree.version);
        return;
      }
      p = this.tree.parentOf(p);
    }
    this.focus.set(this.firstEnabledVisible(), this.tree.version);
  }

  setDisabled(id: NodeId, disabled: boolean): void {
    if (!this.tree.setDisabled(id, disabled)) return;
    if (!disabled) return;
    this.selection.unselect(id);
    if (this.focus.id === id) {
      const list = this.visible.ids;
      const i = list.indexOf(id);
      let target: NodeId | null = null;
      for (let k = i + 1; k < list.length; k++) {
        if (!this.tree.isDisabled(list[k])) {
          target = list[k];
          break;
        }
      }
      if (target === null) {
        for (let k = i - 1; k >= 0; k--) {
          if (!this.tree.isDisabled(list[k])) {
            target = list[k];
            break;
          }
        }
      }
      this.focus.set(target, this.tree.version);
    }
  }

  // ---- async loading -----------------------------------------------------------------

  /** Begin (or restart) an async load. Only the latest load may complete. */
  async loadChildren(
    id: NodeId,
    loader?: (id: NodeId) => Promise<NodeData[]>,
  ): Promise<NodeId[] | null> {
    const fn = loader ?? this.loader;
    if (!fn || !this.tree.exists(id)) return null;
    const token = this.tree.beginLoad(id);
    try {
      const children = await fn(id);
      return this.tree.completeLoad(id, token, children);
    } catch (err) {
      this.tree.failLoad(id, token);
      throw err;
    }
  }

  /** Cancel a pending load: its late result will be discarded. */
  cancelLoad(id: NodeId): void {
    this.tree.cancelLoad(id);
  }
}
