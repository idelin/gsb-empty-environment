/**
 * Minimal editor model: a text buffer plus a list of selections, with
 * indent/unindent operations and byte-exact undo.
 */

import { applyEdits, invertEdits, mapSelections } from "./edits.ts";
import type { Edit, Selection } from "./edits.ts";
import { computeIndentEdits, computeUnindentEdits } from "./indent.ts";
import type { IndentOptions } from "./indent.ts";

interface UndoEntry {
  inverse: Edit[];
  selections: Selection[];
}

export class Editor {
  text: string;
  selections: Selection[];
  private undoStack: UndoEntry[] = [];

  constructor(text: string, selections: Selection[] = []) {
    this.text = text;
    this.selections = selections.map((s) => ({ ...s }));
  }

  get canUndo(): boolean {
    return this.undoStack.length > 0;
  }

  /** Indent every line touched by any selection (each line once). */
  indent(opts: IndentOptions = {}): void {
    this.apply(computeIndentEdits(this.text, this.selections, opts));
  }

  /** Remove up to one tab stop of indentation from every touched line. */
  unindent(opts: IndentOptions = {}): void {
    this.apply(computeUnindentEdits(this.text, this.selections, opts));
  }

  private apply(edits: Edit[]): void {
    if (edits.length === 0) return;
    this.undoStack.push({
      inverse: invertEdits(this.text, edits),
      selections: this.selections.map((s) => ({ ...s })),
    });
    this.text = applyEdits(this.text, edits);
    this.selections = mapSelections(this.selections, edits);
  }

  /**
   * Revert the most recent operation. Restores the previous text
   * byte-for-byte and the previous selections exactly.
   * Returns false when there is nothing to undo.
   */
  undo(): boolean {
    const entry = this.undoStack.pop();
    if (!entry) return false;
    this.text = applyEdits(this.text, entry.inverse);
    this.selections = entry.selections;
    return true;
  }
}
