/**
 * Formal undo history.
 *
 * The history only ever contains FORMAL document states. Composition
 * intermediate states live exclusively in the temporary projection and are
 * never recorded here; a whole composition session contributes at most one
 * undo unit (recorded when it commits).
 *
 * Units are snapshot based: each unit stores the formal text and selection
 * before/after one logical change. Remote edits are rebased positionally
 * into every stored snapshot so undoing local work never erases remote
 * content (deterministic rule: the same positional edit is applied to every
 * snapshot, clamped and surrogate-snapped).
 */

import { applyEdit, transformPoint, type Edit } from './rules.ts';

export interface Selection {
  anchor: number;
  head: number;
}

export interface UndoUnit {
  label: string;
  origin: 'local' | 'composition-commit';
  formalBefore: string;
  formalAfter: string;
  selectionBefore: Selection;
  selectionAfter: Selection;
}

function transformSelection(sel: Selection, edit: Edit): Selection {
  return {
    anchor: transformPoint(sel.anchor, edit),
    head: transformPoint(sel.head, edit),
  };
}

export class History {
  private past: UndoUnit[] = [];
  private future: UndoUnit[] = [];

  record(unit: UndoUnit): void {
    this.past.push(unit);
    this.future = [];
  }

  get undoDepth(): number {
    return this.past.length;
  }

  get redoDepth(): number {
    return this.future.length;
  }

  /** All stored formal snapshots (for invariant checks). */
  snapshots(): string[] {
    const out: string[] = [];
    for (const unit of [...this.past, ...this.future]) {
      out.push(unit.formalBefore, unit.formalAfter);
    }
    return out;
  }

  units(): readonly UndoUnit[] {
    return [...this.past, ...this.future];
  }

  /** Rebase a remote edit through every stored snapshot. */
  rebase(edit: Edit): void {
    for (const unit of [...this.past, ...this.future]) {
      unit.formalBefore = applyEdit(unit.formalBefore, edit);
      unit.formalAfter = applyEdit(unit.formalAfter, edit);
      unit.selectionBefore = transformSelection(unit.selectionBefore, edit);
      unit.selectionAfter = transformSelection(unit.selectionAfter, edit);
    }
  }

  /**
   * Undo the newest unit against the current formal text.
   * Returns the unit and the text/selection to restore, or null.
   */
  popUndo(): UndoUnit | null {
    const unit = this.past.pop();
    if (!unit) return null;
    this.future.push(unit);
    return unit;
  }

  popRedo(): UndoUnit | null {
    const unit = this.future.pop();
    if (!unit) return null;
    this.past.push(unit);
    return unit;
  }
}
