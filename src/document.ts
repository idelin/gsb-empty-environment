/**
 * Core document model.
 *
 * Positions are offsets between characters (0..text.length). An anchor sits
 * on such a boundary and carries an affinity that decides how it behaves when
 * text is inserted exactly at its offset:
 *   - 'left'  stays before the inserted text (offset unchanged)
 *   - 'right' moves after the inserted text (offset += inserted length)
 *
 * A deletion that covers an anchor snaps the anchor to the deletion start.
 * For a replace (delete + insert at the same spot) a covered anchor first
 * snaps to the deletion start, then its affinity is applied against the
 * inserted text.
 */

export type Affinity = 'left' | 'right';

export interface Anchor {
  id: string;
  offset: number;
  affinity: Affinity;
}

/** Normalized half-open range; start <= end. start === end is a caret. */
export interface Selection {
  start: number;
  end: number;
}

export type Edit =
  | { kind: 'insert'; at: number; text: string }
  | { kind: 'delete'; from: number; to: number }
  | { kind: 'replace'; from: number; to: number; text: string };

export interface NormalizedEdit {
  from: number;
  to: number;
  text: string;
}

export function normalizeEdit(edit: Edit): NormalizedEdit {
  switch (edit.kind) {
    case 'insert':
      return { from: edit.at, to: edit.at, text: edit.text };
    case 'delete':
      return { from: edit.from, to: edit.to, text: '' };
    case 'replace':
      return { from: edit.from, to: edit.to, text: edit.text };
  }
}

/**
 * Map a position through a single edit. The edit is treated as a deletion of
 * [from, to) followed by an insertion of `text` at `from`.
 */
export function mapPosition(offset: number, edit: Edit | NormalizedEdit, affinity: Affinity): number {
  const e = 'kind' in edit ? normalizeEdit(edit) : edit;
  const removed = e.to - e.from;
  let pos = offset;
  if (removed > 0) {
    if (pos > e.from && pos < e.to) {
      pos = e.from; // covered by the deletion: snap to deletion start
    } else if (pos >= e.to) {
      pos -= removed;
    }
  }
  const inserted = e.text.length;
  if (inserted > 0) {
    if (pos > e.from || (pos === e.from && affinity === 'right')) {
      pos += inserted;
    }
  }
  return pos;
}

export interface SelectionTransformOptions {
  /** Affinity used for the selection start. Defaults to 'left'. */
  startAffinity?: Affinity;
  /** Affinity used for the selection end. Defaults to 'right'. */
  endAffinity?: Affinity;
}

/**
 * Transform both ends of a selection through an edit. With the defaults an
 * insertion exactly at the start or the end grows the selection; pass the
 * same affinity for both ends to get caret-like behavior for empty
 * selections.
 */
export function transformSelection(
  selection: Selection,
  edit: Edit | NormalizedEdit,
  options: SelectionTransformOptions = {},
): Selection {
  const startAffinity = options.startAffinity ?? 'left';
  const endAffinity = options.endAffinity ?? 'right';
  return {
    start: mapPosition(selection.start, edit, startAffinity),
    end: mapPosition(selection.end, edit, endAffinity),
  };
}

interface IndexedEdit extends NormalizedEdit {
  index: number;
}

/**
 * Validate batch edits (given in original coordinates) and return them in
 * application order: right to left, so that earlier coordinates stay valid.
 * Ties at the same offset apply wider deletions before pure inserts, and
 * same-point inserts apply in reverse array order so their text lands in
 * array order in the final document.
 */
export function sortEditsForApplication(edits: readonly Edit[], docLength: number): IndexedEdit[] {
  const normalized: IndexedEdit[] = edits.map((edit, index) => {
    const e = normalizeEdit(edit);
    if (!Number.isInteger(e.from) || !Number.isInteger(e.to) || e.from < 0 || e.from > e.to || e.to > docLength) {
      throw new RangeError(
        `edit #${index} has invalid range [${e.from}, ${e.to}) for document of length ${docLength}`,
      );
    }
    return { ...e, index };
  });
  const byStart = [...normalized].sort((a, b) => a.from - b.from || a.index - b.index);
  for (let i = 1; i < byStart.length; i++) {
    const prev = byStart[i - 1];
    const cur = byStart[i];
    const prevEmpty = prev.from === prev.to;
    const curEmpty = cur.from === cur.to;
    // Zero-width edits only conflict when strictly inside another range;
    // sitting on a boundary (or sharing a point) is well-defined.
    let conflict: boolean;
    if (prevEmpty) {
      conflict = false; // cur starts at or after the point prev.from === prev.to
    } else if (curEmpty) {
      conflict = prev.from < cur.from && cur.from < prev.to;
    } else {
      conflict = cur.from < prev.to;
    }
    if (conflict) {
      throw new Error(
        `overlapping edits: #${prev.index} [${prev.from}, ${prev.to}) ` +
          `and #${cur.index} [${cur.from}, ${cur.to})`,
      );
    }
  }
  return normalized.sort(
    (a, b) => b.from - a.from || b.to - b.from - (a.to - a.from) || b.index - a.index,
  );
}

interface Snapshot {
  text: string;
  anchors: Anchor[];
  selection: Selection | null;
}

export class Document {
  #text: string;
  #anchors = new Map<string, Anchor>();
  #selection: Selection | null = null;
  #undoStack: Snapshot[] = [];
  #redoStack: Snapshot[] = [];

  constructor(text = '') {
    this.#text = text;
  }

  get text(): string {
    return this.#text;
  }

  get length(): number {
    return this.#text.length;
  }

  get selection(): Selection | null {
    return this.#selection ? { ...this.#selection } : null;
  }

  setSelection(start: number, end: number): void {
    this.#assertOffset(start);
    this.#assertOffset(end);
    this.#selection = start <= end ? { start, end } : { start: end, end: start };
  }

  clearSelection(): void {
    this.#selection = null;
  }

  addAnchor(id: string, offset: number, affinity: Affinity = 'right'): void {
    if (this.#anchors.has(id)) {
      throw new Error(`duplicate anchor id: ${id}`);
    }
    this.#assertOffset(offset);
    this.#anchors.set(id, { id, offset, affinity });
  }

  removeAnchor(id: string): boolean {
    return this.#anchors.delete(id);
  }

  getAnchor(id: string): Anchor | undefined {
    const anchor = this.#anchors.get(id);
    return anchor ? { ...anchor } : undefined;
  }

  anchors(): Anchor[] {
    return [...this.#anchors.values()]
      .map((anchor) => ({ ...anchor }))
      .sort((a, b) => a.offset - b.offset || (a.id < b.id ? -1 : 1));
  }

  /** Apply a single edit as one undoable step. */
  applyEdit(edit: Edit, options?: SelectionTransformOptions): void {
    this.applyBatch([edit], options);
  }

  /**
   * Apply a batch of edits expressed in original (pre-edit) coordinates.
   * Edits must not overlap; touching ranges and same-point inserts are fine.
   * The whole batch is a single undoable step.
   */
  applyBatch(edits: readonly Edit[], options: SelectionTransformOptions = {}): void {
    if (edits.length === 0) return;
    const ordered = sortEditsForApplication(edits, this.#text.length);
    this.#pushUndo();
    for (const edit of ordered) {
      this.#applyNormalized(edit, options);
    }
  }

  undo(): boolean {
    const snapshot = this.#undoStack.pop();
    if (!snapshot) return false;
    this.#redoStack.push(this.#takeSnapshot());
    this.#restore(snapshot);
    return true;
  }

  redo(): boolean {
    const snapshot = this.#redoStack.pop();
    if (!snapshot) return false;
    this.#undoStack.push(this.#takeSnapshot());
    this.#restore(snapshot);
    return true;
  }

  get canUndo(): boolean {
    return this.#undoStack.length > 0;
  }

  get canRedo(): boolean {
    return this.#redoStack.length > 0;
  }

  #applyNormalized(edit: NormalizedEdit, options: SelectionTransformOptions): void {
    this.#text = this.#text.slice(0, edit.from) + edit.text + this.#text.slice(edit.to);
    for (const anchor of this.#anchors.values()) {
      anchor.offset = mapPosition(anchor.offset, edit, anchor.affinity);
    }
    if (this.#selection) {
      this.#selection = transformSelection(this.#selection, edit, options);
    }
  }

  #assertOffset(offset: number): void {
    if (!Number.isInteger(offset) || offset < 0 || offset > this.#text.length) {
      throw new RangeError(`offset ${offset} out of bounds for document of length ${this.#text.length}`);
    }
  }

  #pushUndo(): void {
    this.#undoStack.push(this.#takeSnapshot());
    this.#redoStack = [];
  }

  #takeSnapshot(): Snapshot {
    return {
      text: this.#text,
      anchors: [...this.#anchors.values()].map((anchor) => ({ ...anchor })),
      selection: this.#selection ? { ...this.#selection } : null,
    };
  }

  #restore(snapshot: Snapshot): void {
    this.#text = snapshot.text;
    this.#anchors = new Map(snapshot.anchors.map((anchor) => [anchor.id, { ...anchor }]));
    this.#selection = snapshot.selection ? { ...snapshot.selection } : null;
  }
}
