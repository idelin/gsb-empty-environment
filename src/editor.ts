/**
 * Atomic multi-cursor edit transactions.
 *
 * A transaction stages text edits, selection changes and annotation changes
 * against ONE baseline coordinate system (the document state when the
 * transaction began). Committing validates everything up front — conflicting
 * (overlapping) replacements fail before a single character is written —
 * and any failure, including an exception thrown by the caller's callback,
 * restores text, selections, annotations and the undo stack to their exact
 * pre-transaction state.
 */

import {
  assertOffset,
  assertRange,
  type Position,
  offsetToPosition,
  positionToOffset,
} from './offsets.ts';
import {
  applyEdits,
  assertNoConflicts,
  mapOffset,
  mapRange,
  validateEdits,
  type Edit,
  type Range,
} from './transaction.ts';

export interface Selection {
  /** Where the selection started (may be after `head`). */
  anchor: number;
  /** The caret end of the selection. */
  head: number;
}

export interface Annotation {
  id: string;
  range: Range;
  data?: unknown;
}

export interface UndoRecord {
  /** Inverse edits, in the coordinate system of the text they apply to. */
  inverseEdits: Edit[];
  selectionsBefore: Selection[];
  annotationsBefore: Annotation[];
}

interface Snapshot {
  text: string;
  selections: Selection[];
  annotations: Annotation[];
  undoDepth: number;
}

function cloneSelections(selections: readonly Selection[]): Selection[] {
  return selections.map((s) => ({ anchor: s.anchor, head: s.head }));
}

function cloneAnnotations(annotations: readonly Annotation[]): Annotation[] {
  return annotations.map((a) => ({
    id: a.id,
    range: { start: a.range.start, end: a.range.end },
    data: a.data,
  }));
}

/**
 * Staging area handed to the transaction callback. All coordinates are
 * expressed against the transaction's baseline text.
 */
export class Transaction {
  readonly #baseline: string;
  readonly #edits: Edit[] = [];
  #stagedSelections: Selection[] | undefined;
  readonly #addedAnnotations: Annotation[] = [];
  readonly #removedAnnotationIds = new Set<string>();

  constructor(baseline: string) {
    this.#baseline = baseline;
  }

  /** The text as it was when the transaction started. */
  get baselineText(): string {
    return this.#baseline;
  }

  /** Edits staged so far, in staging order. */
  get edits(): readonly Edit[] {
    return this.#edits;
  }

  /** Replace the half-open baseline range [start, end) with `text`. */
  replace(start: number, end: number, text: string): this {
    assertRange(this.#baseline, start, end, 'replace range');
    this.#edits.push({ range: { start, end }, text });
    return this;
  }

  /** Insert `text` at a baseline offset. */
  insert(offset: number, text: string): this {
    assertOffset(this.#baseline, offset, 'insert offset');
    this.#edits.push({ range: { start: offset, end: offset }, text });
    return this;
  }

  /** Delete the half-open baseline range [start, end). */
  delete(start: number, end: number): this {
    assertRange(this.#baseline, start, end, 'delete range');
    this.#edits.push({ range: { start, end }, text: '' });
    return this;
  }

  /**
   * Stage selections, expressed in baseline coordinates. They are validated
   * against the baseline and mapped through the staged edits at commit time.
   */
  setSelections(selections: readonly Selection[]): this {
    for (const sel of selections) {
      assertOffset(this.#baseline, sel.anchor, 'selection.anchor');
      assertOffset(this.#baseline, sel.head, 'selection.head');
    }
    this.#stagedSelections = cloneSelections(selections);
    return this;
  }

  /** Stage a new annotation (baseline coordinates, mapped at commit). */
  addAnnotation(annotation: Annotation): this {
    assertRange(
      this.#baseline,
      annotation.range.start,
      annotation.range.end,
      'annotation.range',
    );
    this.#addedAnnotations.push({
      id: annotation.id,
      range: { start: annotation.range.start, end: annotation.range.end },
      data: annotation.data,
    });
    return this;
  }

  /** Stage removal of an annotation by id. */
  removeAnnotation(id: string): this {
    this.#removedAnnotationIds.add(id);
    return this;
  }

  /** @internal */
  commitState(): {
    edits: Edit[];
    stagedSelections: Selection[] | undefined;
    addedAnnotations: Annotation[];
    removedAnnotationIds: Set<string>;
  } {
    return {
      edits: [...this.#edits],
      stagedSelections: this.#stagedSelections
        ? cloneSelections(this.#stagedSelections)
        : undefined,
      addedAnnotations: cloneAnnotations(this.#addedAnnotations),
      removedAnnotationIds: new Set(this.#removedAnnotationIds),
    };
  }
}

export class Editor {
  #text: string;
  #selections: Selection[] = [];
  #annotations: Annotation[] = [];
  #undoStack: UndoRecord[] = [];

  constructor(initialText = '') {
    this.#text = initialText;
  }

  get text(): string {
    return this.#text;
  }

  get selections(): readonly Selection[] {
    return this.#selections;
  }

  get annotations(): readonly Annotation[] {
    return this.#annotations;
  }

  get undoDepth(): number {
    return this.#undoStack.length;
  }

  get canUndo(): boolean {
    return this.#undoStack.length > 0;
  }

  /** Replace the whole document outside of a transaction (initial load). */
  setText(text: string): void {
    this.#text = text;
    this.#selections = [];
    this.#annotations = [];
    this.#undoStack = [];
  }

  setSelections(selections: readonly Selection[]): void {
    for (const sel of selections) {
      assertOffset(this.#text, sel.anchor, 'selection.anchor');
      assertOffset(this.#text, sel.head, 'selection.head');
    }
    this.#selections = cloneSelections(selections);
  }

  addAnnotation(annotation: Annotation): void {
    assertRange(
      this.#text,
      annotation.range.start,
      annotation.range.end,
      'annotation.range',
    );
    if (this.#annotations.some((a) => a.id === annotation.id)) {
      throw new Error(`duplicate annotation id: ${annotation.id}`);
    }
    this.#annotations.push({
      id: annotation.id,
      range: { start: annotation.range.start, end: annotation.range.end },
      data: annotation.data,
    });
  }

  removeAnnotation(id: string): void {
    this.#annotations = this.#annotations.filter((a) => a.id !== id);
  }

  positionAt(offset: number): Position {
    return offsetToPosition(this.#text, offset);
  }

  offsetAt(position: Position): number {
    return positionToOffset(this.#text, position);
  }

  /**
   * Run `callback` as one atomic transaction.
   *
   * The callback receives a Transaction to stage edits, selections and
   * annotation changes in baseline coordinates. On success everything is
   * committed and one undo record is pushed. If the callback throws, or any
   * commit step fails (validation, conflict detection, ...), the document,
   * selections, annotations and undo stack are restored to their exact
   * pre-transaction state and the error is rethrown.
   */
  transaction(callback: (tx: Transaction) => void): void {
    const snapshot: Snapshot = {
      text: this.#text,
      selections: cloneSelections(this.#selections),
      annotations: cloneAnnotations(this.#annotations),
      undoDepth: this.#undoStack.length,
    };
    const tx = new Transaction(snapshot.text);
    try {
      callback(tx);
      this.#commit(tx, snapshot);
    } catch (error) {
      this.#text = snapshot.text;
      this.#selections = snapshot.selections;
      this.#annotations = snapshot.annotations;
      this.#undoStack.length = snapshot.undoDepth;
      throw error;
    }
  }

  #commit(tx: Transaction, snapshot: Snapshot): void {
    const { edits, stagedSelections, addedAnnotations, removedAnnotationIds } =
      tx.commitState();

    // Validate EVERYTHING against the baseline before touching any state.
    validateEdits(this.#text, edits);
    assertNoConflicts(edits);

    const { text, inverse } = applyEdits(this.#text, edits);

    // Selections: staged ones win; otherwise map the current selections
    // through the edits so cursors track the surrounding text.
    const sourceSelections = stagedSelections ?? this.#selections;
    const nextSelections = sourceSelections.map((sel) => ({
      anchor: mapOffset(sel.anchor, edits, 'start'),
      head: mapOffset(sel.head, edits, 'start'),
    }));

    // Annotations: map existing ranges, apply removals and additions.
    const nextAnnotations: Annotation[] = [];
    for (const ann of this.#annotations) {
      if (removedAnnotationIds.has(ann.id)) continue;
      nextAnnotations.push({
        id: ann.id,
        range: mapRange(ann.range, edits),
        data: ann.data,
      });
    }
    for (const ann of addedAnnotations) {
      if (nextAnnotations.some((a) => a.id === ann.id)) {
        throw new Error(`duplicate annotation id: ${ann.id}`);
      }
      nextAnnotations.push({
        id: ann.id,
        range: mapRange(ann.range, edits),
        data: ann.data,
      });
    }

    // All validation done — publish the new state atomically and record
    // one undo entry capturing the pre-transaction state.
    this.#text = text;
    this.#selections = nextSelections;
    this.#annotations = nextAnnotations;
    this.#undoStack.push({
      inverseEdits: inverse,
      selectionsBefore: snapshot.selections,
      annotationsBefore: snapshot.annotations,
    });
  }

  /** Undo the most recently committed transaction. Returns false if none. */
  undo(): boolean {
    const record = this.#undoStack.pop();
    if (!record) return false;
    const { text } = applyEdits(this.#text, record.inverseEdits);
    this.#text = text;
    this.#selections = cloneSelections(record.selectionsBefore);
    this.#annotations = cloneAnnotations(record.annotationsBefore);
    return true;
  }
}
