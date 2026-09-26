/** Shared drag state machine used by BOTH mouse and keyboard drivers.
 *
 *  Guarantees:
 *  - Preview and commit are separated: the preview tree is derived, the
 *    canonical tree is never touched by a drag.
 *  - A node can never be dropped into its own descendant.
 *  - Cancel (or abnormal termination) restores the full UI state.
 *  - The logical op trace is identical for equivalent mouse/keyboard
 *    gestures, because both drivers funnel into the same intents. */
import type { Location, MovePatch, NodeId, TraceEntry, Tree, UiState } from './types.ts';
import { applyMove, cloneTree, locate, validateMove } from './tree.ts';

export type Phase = 'idle' | 'dragging' | 'awaiting';

function cloneUi(ui: UiState): UiState {
  return { focusId: ui.focusId, selection: [...ui.selection] };
}

export class DragMachine {
  ui: UiState;
  readonly trace: TraceEntry[] = [];
  private canonical: Tree;
  private phaseValue: Phase = 'idle';
  private draggedId: NodeId | null = null;
  /** Normalized target in post-removal coordinates. */
  private target: Location | null = null;
  private uiSnapshot: UiState | null = null;

  constructor(tree: Tree, ui: UiState = { focusId: null, selection: [] }) {
    this.canonical = tree;
    this.ui = cloneUi(ui);
  }

  get phase(): Phase { return this.phaseValue; }
  get canonicalTree(): Tree { return this.canonical; }
  get draggedNodeId(): NodeId | null { return this.draggedId; }
  get currentTarget(): Location | null { return this.target ? { ...this.target } : null; }

  /** The canonical tree with the dragged node detached. Keyboard drivers
   *  and hit-test helpers compute targets in this coordinate space. */
  detachedTree(): Tree {
    if (this.phaseValue !== 'dragging' || this.draggedId === null) return this.canonical;
    const t = cloneTree(this.canonical);
    const from = locate(t, this.draggedId);
    t.nodes[from.parentId].children.splice(from.index, 1);
    return t;
  }

  /** Derived preview. The canonical tree is never modified. */
  previewTree(): Tree {
    if (this.phaseValue !== 'dragging' || this.draggedId === null || this.target === null) {
      return this.canonical;
    }
    return applyMove(this.canonical, this.draggedId, this.target).tree;
  }

  start(nodeId: NodeId): boolean {
    if (this.phaseValue !== 'idle') return false;
    if (!this.canonical.nodes[nodeId] || nodeId === this.canonical.rootId) return false;
    this.phaseValue = 'dragging';
    this.draggedId = nodeId;
    this.uiSnapshot = cloneUi(this.ui);
    this.ui = { focusId: nodeId, selection: [nodeId] };
    this.target = locate(this.canonical, nodeId); // identity target
    this.trace.push({ op: 'start', nodeId });
    return true;
  }

  /** Accept a target in pre-removal coordinates (mouse hit-testing). */
  setTarget(loc: Location): boolean {
    if (this.phaseValue !== 'dragging' || this.draggedId === null) return false;
    const from = locate(this.canonical, this.draggedId);
    let index = loc.index;
    if (loc.parentId === from.parentId && index > from.index) index -= 1;
    return this.setTargetNormalized({ parentId: loc.parentId, index });
  }

  /** Accept a target in post-removal coordinates (keyboard drivers). */
  setTargetNormalized(loc: Location): boolean {
    if (this.phaseValue !== 'dragging' || this.draggedId === null) return false;
    // Rejects out-of-range targets and drops into the node's own subtree.
    if (validateMove(this.canonical, this.draggedId, loc) !== null) return false;
    if (this.target && this.target.parentId === loc.parentId && this.target.index === loc.index) {
      return true; // no duplicate trace entry
    }
    this.target = { parentId: loc.parentId, index: loc.index };
    this.trace.push({ op: 'target', parentId: loc.parentId, index: loc.index });
    return true;
  }

  /** Seal the drag into a patch. The caller wraps it in a CommitOp. */
  commit(): MovePatch | null {
    if (this.phaseValue !== 'dragging' || this.draggedId === null || this.target === null) return null;
    const from = locate(this.canonical, this.draggedId);
    const patch: MovePatch = { kind: 'move', nodeId: this.draggedId, from, to: { ...this.target } };
    this.phaseValue = 'awaiting';
    this.trace.push({ op: 'commit', patch });
    return patch;
  }

  /** Resolve a previously issued commit. */
  resolveCommit(ok: boolean): void {
    if (this.phaseValue !== 'awaiting' || this.draggedId === null) return;
    this.ui = ok ? { focusId: this.draggedId, selection: [this.draggedId] } : (this.uiSnapshot ?? this.ui);
    this.reset();
  }

  cancel(): void {
    if (this.phaseValue !== 'dragging') return;
    this.trace.push({ op: 'cancel' });
    this.ui = this.uiSnapshot ?? this.ui;
    this.reset();
  }

  /** Abnormal termination, e.g. the dragged node or the drop target was
   *  deleted by a server broadcast mid-drag. Restores all UI state and
   *  leaves the canonical tree untouched. */
  terminate(reason: string): void {
    if (this.phaseValue === 'idle') return;
    this.trace.push({ op: 'terminate', reason });
    this.ui = this.uiSnapshot ?? this.ui;
    this.reset();
  }

  /** Swap in a new canonical tree (server broadcast). A live drag is
   *  validated against it and safely terminated if it became impossible. */
  rebase(tree: Tree): void {
    this.canonical = tree;
    if (this.phaseValue !== 'dragging' || this.draggedId === null || this.target === null) return;
    if (!tree.nodes[this.draggedId]) {
      this.terminate('dragged-node-removed');
    } else if (!tree.nodes[this.target.parentId]) {
      this.terminate('target-removed');
    } else if (validateMove(tree, this.draggedId, this.target) !== null) {
      this.terminate('target-invalid');
    }
  }

  private reset(): void {
    this.phaseValue = 'idle';
    this.draggedId = null;
    this.target = null;
    this.uiSnapshot = null;
  }
}
