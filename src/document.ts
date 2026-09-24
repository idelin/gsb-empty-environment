/**
 * Atomic multi-cursor edit transactions.
 *
 * Core ideas:
 * - All ranges handed to a transaction are interpreted in ONE baseline
 *   coordinate system: the document text as it was when the transaction
 *   started. Edits never shift each other's coordinates.
 * - Offsets are UTF-16 code unit indices (same as JS string indexing).
 *   Edit boundaries that would split a surrogate pair are rejected.
 * - Overlapping replacements are detected BEFORE anything is written.
 * - A transaction updates text, selections, annotations and the undo
 *   stack atomically: if any step fails (validation or a throwing
 *   callback), all four are restored to their exact pre-transaction state.
 */

export interface Range {
  readonly start: number;
  readonly end: number;
}

export interface Selection {
  readonly anchor: number;
  readonly head: number;
}

export interface Annotation {
  readonly id: string;
  readonly range: Range;
  readonly data?: unknown;
}

export interface Position {
  readonly line: number;
  readonly character: number;
}

export class EditConflictError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "EditConflictError";
  }
}

export class SurrogatePairError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SurrogatePairError";
  }
}

export class EditOutOfBoundsError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "EditOutOfBoundsError";
  }
}

export class NestedTransactionError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "NestedTransactionError";
  }
}

export class TransactionStateError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "TransactionStateError";
  }
}

type Bias = "start" | "end";

interface Edit {
  start: number;
  end: number;
  text: string;
  seq: number;
}

interface StateSnapshot {
  text: string;
  selections: Selection[];
  annotations: Annotation[];
}

interface TransactionSnapshot extends StateSnapshot {
  undoDepth: number;
}

const HIGH_SURROGATE_MIN = 0xd800;
const HIGH_SURROGATE_MAX = 0xdbff;
const LOW_SURROGATE_MIN = 0xdc00;
const LOW_SURROGATE_MAX = 0xdfff;

function isHighSurrogate(code: number): boolean {
  return code >= HIGH_SURROGATE_MIN && code <= HIGH_SURROGATE_MAX;
}

function isLowSurrogate(code: number): boolean {
  return code >= LOW_SURROGATE_MIN && code <= LOW_SURROGATE_MAX;
}

/** True when `offset` falls between a high and a low surrogate in `text`. */
function splitsSurrogatePair(text: string, offset: number): boolean {
  return (
    offset > 0 &&
    offset < text.length &&
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset))
  );
}

function assertValidOffset(value: number, name: string): void {
  if (!Number.isInteger(value) || value < 0) {
    throw new EditOutOfBoundsError(
      `${name} must be a non-negative integer, got ${String(value)}`,
    );
  }
}

function cloneSelections(selections: readonly Selection[]): Selection[] {
  return selections.map((s) => ({ anchor: s.anchor, head: s.head }));
}

function cloneAnnotations(annotations: readonly Annotation[]): Annotation[] {
  return annotations.map((a) => ({ ...a, range: { ...a.range } }));
}

/**
 * Apply non-overlapping edits (sorted ascending by start) to `text`.
 * Because edits are disjoint and refer to baseline coordinates, applying
 * them in a single left-to-right pass keeps every range correct: a length
 * change in a later replacement can never shift an earlier range.
 */
function applyEdits(text: string, sortedEdits: readonly Edit[]): string {
  let out = "";
  let cursor = 0;
  for (const edit of sortedEdits) {
    out += text.slice(cursor, edit.start) + edit.text;
    cursor = edit.end;
  }
  return out + text.slice(cursor);
}

/**
 * Map a baseline offset through the applied edits into post-edit
 * coordinates. `bias` decides where positions inside a replaced range (or
 * exactly at an insertion point) land: at the start or the end of the
 * replacement text.
 */
function mapPosition(
  position: number,
  sortedEdits: readonly Edit[],
  bias: Bias,
): number {
  let delta = 0;
  for (const edit of sortedEdits) {
    if (position < edit.start) break;
    if (position > edit.end) {
      delta += edit.text.length - (edit.end - edit.start);
      continue;
    }
    if (edit.start === edit.end) {
      // Pure insertion exactly at `position`.
      if (bias === "end") {
        delta += edit.text.length;
        continue;
      }
      break;
    }
    // Position lies inside a replaced range: collapse to the
    // corresponding edge of the replacement text.
    return bias === "end"
      ? edit.start + delta + edit.text.length
      : edit.start + delta;
  }
  return position + delta;
}

/**
 * Map a selection through the applied edits. A collapsed cursor rides to
 * the end of text inserted at its position (typing moves the cursor). A
 * non-collapsed selection keeps covering the content it spanned: its
 * leading edge maps with "start" bias, its trailing edge with "end" bias,
 * so a selection covering a replaced range covers the replacement.
 */
function mapSelection(sel: Selection, sortedEdits: readonly Edit[]): Selection {
  if (sel.anchor === sel.head) {
    const mapped = mapPosition(sel.anchor, sortedEdits, "end");
    return { anchor: mapped, head: mapped };
  }
  const anchorBias: Bias = sel.anchor < sel.head ? "start" : "end";
  const headBias: Bias = sel.anchor < sel.head ? "end" : "start";
  return {
    anchor: mapPosition(sel.anchor, sortedEdits, anchorBias),
    head: mapPosition(sel.head, sortedEdits, headBias),
  };
}

function computeLineStarts(text: string): number[] {
  const starts = [0];
  for (let i = 0; i < text.length; i++) {
    const code = text.charCodeAt(i);
    if (code === 0x0d /* \r */) {
      if (i + 1 < text.length && text.charCodeAt(i + 1) === 0x0a /* \n */) {
        i++;
      }
      starts.push(i + 1);
    } else if (code === 0x0a /* \n */) {
      starts.push(i + 1);
    }
  }
  return starts;
}

export class EditorDocument {
  private text: string;
  private selections: Selection[] = [];
  private annotations: Annotation[] = [];
  private undoStack: StateSnapshot[] = [];
  private inTransaction = false;

  constructor(text = "") {
    this.text = text;
  }

  getText(): string {
    return this.text;
  }

  get length(): number {
    return this.text.length;
  }

  getSelections(): Selection[] {
    return cloneSelections(this.selections);
  }

  getAnnotations(): Annotation[] {
    return cloneAnnotations(this.annotations);
  }

  get undoDepth(): number {
    return this.undoStack.length;
  }

  /** Non-transactional setter, intended for fixture/setup code. */
  setSelections(selections: readonly Selection[]): void {
    for (const sel of selections) {
      assertValidOffset(sel.anchor, "selection anchor");
      assertValidOffset(sel.head, "selection head");
      if (sel.anchor > this.text.length || sel.head > this.text.length) {
        throw new EditOutOfBoundsError(
          `selection [${sel.anchor}, ${sel.head}] exceeds document length ${this.text.length}`,
        );
      }
    }
    this.selections = cloneSelections(selections);
  }

  /** Non-transactional setter, intended for fixture/setup code. */
  setAnnotations(annotations: readonly Annotation[]): void {
    for (const ann of annotations) {
      this.assertValidAnnotation(ann);
    }
    this.annotations = cloneAnnotations(annotations);
  }

  /**
   * Run `fn` as one atomic transaction. All edits staged through the
   * transaction are interpreted in the baseline coordinate system (the
   * text as it is right now). On any failure — validation errors or an
   * exception thrown by `fn` — text, selections, annotations and the
   * undo stack are restored to their exact pre-transaction state.
   */
  transact<T>(fn: (tx: Transaction) => T): T {
    if (this.inTransaction) {
      throw new NestedTransactionError(
        "nested transactions are not supported",
      );
    }
    this.inTransaction = true;
    const snapshot: TransactionSnapshot = {
      text: this.text,
      selections: cloneSelections(this.selections),
      annotations: cloneAnnotations(this.annotations),
      undoDepth: this.undoStack.length,
    };
    const tx = new Transaction(this);
    try {
      const result = fn(tx);
      tx.commit();
      return result;
    } catch (error) {
      this.text = snapshot.text;
      this.selections = snapshot.selections;
      this.annotations = snapshot.annotations;
      this.undoStack.length = snapshot.undoDepth;
      throw error;
    } finally {
      this.inTransaction = false;
    }
  }

  /** Restore the state captured by the most recent successful transaction. */
  undo(): boolean {
    const snapshot = this.undoStack.pop();
    if (!snapshot) return false;
    this.text = snapshot.text;
    this.selections = cloneSelections(snapshot.selections);
    this.annotations = cloneAnnotations(snapshot.annotations);
    return true;
  }

  /** Convert a UTF-16 offset to a {line, character} position. Handles LF, CRLF and CR. */
  positionAt(offset: number): Position {
    assertValidOffset(offset, "offset");
    const clamped = Math.min(offset, this.text.length);
    const starts = computeLineStarts(this.text);
    let low = 0;
    let high = starts.length - 1;
    while (low < high) {
      const mid = Math.ceil((low + high) / 2);
      if (starts[mid] <= clamped) low = mid;
      else high = mid - 1;
    }
    return { line: low, character: clamped - starts[low] };
  }

  /** Convert a {line, character} position to a UTF-16 offset. Handles LF, CRLF and CR. */
  offsetAt(position: Position): number {
    const starts = computeLineStarts(this.text);
    const line = Math.min(Math.max(0, position.line), starts.length - 1);
    const lineStart = starts[line];
    const lineEnd =
      line + 1 < starts.length ? starts[line + 1] : this.text.length;
    // Exclude the line break itself (\n or \r\n) from the writable range.
    let contentEnd = lineEnd;
    if (contentEnd > lineStart && this.text.charCodeAt(contentEnd - 1) === 0x0a) {
      contentEnd--;
      if (contentEnd > lineStart && this.text.charCodeAt(contentEnd - 1) === 0x0d) {
        contentEnd--;
      }
    } else if (contentEnd > lineStart && this.text.charCodeAt(contentEnd - 1) === 0x0d) {
      contentEnd--;
    }
    return Math.min(lineStart + Math.max(0, position.character), contentEnd);
  }

  get lineCount(): number {
    return computeLineStarts(this.text).length;
  }

  private assertValidAnnotation(annotation: Annotation): void {
    if (typeof annotation.id !== "string" || annotation.id.length === 0) {
      throw new EditOutOfBoundsError("annotation id must be a non-empty string");
    }
    assertValidOffset(annotation.range.start, "annotation start");
    assertValidOffset(annotation.range.end, "annotation end");
    if (annotation.range.start > annotation.range.end) {
      throw new EditOutOfBoundsError(
        `annotation range start ${annotation.range.start} > end ${annotation.range.end}`,
      );
    }
    if (annotation.range.end > this.text.length) {
      throw new EditOutOfBoundsError(
        `annotation range [${annotation.range.start}, ${annotation.range.end}] exceeds document length ${this.text.length}`,
      );
    }
  }

  /** @internal Called by Transaction only. */
  getBaselineText(): string {
    return this.text;
  }

  /** @internal Called by Transaction only. */
  assertValidAnnotationForTransaction(annotation: Annotation): void {
    this.assertValidAnnotation(annotation);
  }

  /** @internal Called by Transaction only, after all validation passed. */
  commitTransaction(
    newText: string,
    selections: Selection[],
    annotations: Annotation[],
  ): void {
    const undoSnapshot: StateSnapshot = {
      text: this.text,
      selections: cloneSelections(this.selections),
      annotations: cloneAnnotations(this.annotations),
    };
    this.text = newText;
    this.selections = selections;
    this.annotations = annotations;
    this.undoStack.push(undoSnapshot);
  }
}

export class Transaction {
  private readonly doc: EditorDocument;
  private readonly baselineLength: number;
  private edits: Edit[] = [];
  private seq = 0;
  private stagedSelections: Selection[] | null = null;
  private annotationWork: Annotation[] | null = null;
  private finished = false;

  /** @internal Transactions are created by EditorDocument.transact. */
  constructor(doc: EditorDocument) {
    this.doc = doc;
    this.baselineLength = doc.getBaselineText().length;
  }

  /**
   * Stage a replacement of baseline range [start, end) with `text`.
   * Coordinates always refer to the pre-transaction text, never to the
   * result of other staged edits.
   */
  replace(start: number, end: number, text: string): this {
    this.assertActive();
    assertValidOffset(start, "replace start");
    assertValidOffset(end, "replace end");
    if (start > end) {
      throw new EditOutOfBoundsError(
        `replace start ${start} > end ${end}`,
      );
    }
    if (end > this.baselineLength) {
      throw new EditOutOfBoundsError(
        `replace range [${start}, ${end}] exceeds document length ${this.baselineLength}`,
      );
    }
    const baseline = this.doc.getBaselineText();
    if (splitsSurrogatePair(baseline, start)) {
      throw new SurrogatePairError(
        `replace start ${start} splits a surrogate pair`,
      );
    }
    if (splitsSurrogatePair(baseline, end)) {
      throw new SurrogatePairError(
        `replace end ${end} splits a surrogate pair`,
      );
    }
    if (text.length > 0) {
      if (isLowSurrogate(text.charCodeAt(0))) {
        throw new SurrogatePairError(
          "replacement text starts with an unpaired low surrogate",
        );
      }
      if (isHighSurrogate(text.charCodeAt(text.length - 1))) {
        throw new SurrogatePairError(
          "replacement text ends with an unpaired high surrogate",
        );
      }
    }
    this.edits.push({ start, end, text, seq: this.seq++ });
    return this;
  }

  insert(at: number, text: string): this {
    return this.replace(at, at, text);
  }

  delete(start: number, end: number): this {
    return this.replace(start, end, "");
  }

  /**
   * Stage new selections. Offsets are interpreted in the baseline
   * coordinate system and are remapped through the staged edits on commit.
   */
  setSelections(selections: readonly Selection[]): this {
    this.assertActive();
    for (const sel of selections) {
      assertValidOffset(sel.anchor, "selection anchor");
      assertValidOffset(sel.head, "selection head");
      if (sel.anchor > this.baselineLength || sel.head > this.baselineLength) {
        throw new EditOutOfBoundsError(
          `selection [${sel.anchor}, ${sel.head}] exceeds document length ${this.baselineLength}`,
        );
      }
    }
    this.stagedSelections = cloneSelections(selections);
    return this;
  }

  addAnnotation(annotation: Annotation): this {
    this.assertActive();
    this.doc.assertValidAnnotationForTransaction(annotation);
    this.ensureAnnotationWork().push({ ...annotation, range: { ...annotation.range } });
    return this;
  }

  removeAnnotation(id: string): this {
    this.assertActive();
    const work = this.ensureAnnotationWork();
    const index = work.findIndex((a) => a.id === id);
    if (index >= 0) work.splice(index, 1);
    return this;
  }

  setAnnotations(annotations: readonly Annotation[]): this {
    this.assertActive();
    for (const ann of annotations) {
      this.doc.assertValidAnnotationForTransaction(ann);
    }
    this.annotationWork = cloneAnnotations(annotations);
    return this;
  }

  /** @internal Called by EditorDocument.transact only. */
  commit(): void {
    this.assertActive();
    this.finished = true;

    const sorted = [...this.edits].sort(
      (a, b) => a.start - b.start || a.seq - b.seq,
    );
    // Conflict detection happens BEFORE anything is written.
    for (let i = 1; i < sorted.length; i++) {
      const prev = sorted[i - 1];
      const curr = sorted[i];
      if (curr.start < prev.end) {
        throw new EditConflictError(
          `overlapping edits: [${prev.start}, ${prev.end}) and [${curr.start}, ${curr.end})`,
        );
      }
    }

    const changed =
      sorted.length > 0 ||
      this.stagedSelections !== null ||
      this.annotationWork !== null;
    if (!changed) return;

    const baseline = this.doc.getBaselineText();
    const newText = applyEdits(baseline, sorted);

    const baseSelections = this.stagedSelections ?? this.doc.getSelections();
    const selections = baseSelections.map((sel) =>
      mapSelection(sel, sorted),
    );

    const baseAnnotations = this.annotationWork ?? this.doc.getAnnotations();
    const annotations = baseAnnotations.map((ann) => {
      const start = mapPosition(ann.range.start, sorted, "start");
      let end = mapPosition(ann.range.end, sorted, "end");
      if (end < start) end = start;
      return { ...ann, range: { start, end } };
    });

    this.doc.commitTransaction(newText, selections, annotations);
  }

  private ensureAnnotationWork(): Annotation[] {
    if (this.annotationWork === null) {
      this.annotationWork = this.doc.getAnnotations();
    }
    return this.annotationWork;
  }

  private assertActive(): void {
    if (this.finished) {
      throw new TransactionStateError("transaction is already finished");
    }
  }
}
