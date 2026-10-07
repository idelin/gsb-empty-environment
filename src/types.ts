/** A half-open range [start, end) expressed as character offsets. */
export interface Range {
  start: number;
  end: number;
}

/**
 * A single edit against a frozen document version: replace the half-open
 * range [start, end) with `newText`. A zero-length range is a pure insertion;
 * an empty `newText` is a pure deletion.
 */
export interface TextEdit {
  start: number;
  end: number;
  newText: string;
}

/** The inverse of a TextEdit, sufficient to restore the previous text. */
export interface InverseEdit {
  start: number;
  end: number;
  /** Text that occupied [start, end) after the forward edit was applied. */
  oldText: string;
}

export type Bias = 'before' | 'after';

/**
 * Maps character offsets between the old (pre-edit) and new (post-edit)
 * text of a single document.
 */
export interface PositionMap {
  oldLength: number;
  newLength: number;
  /** Map an offset in the old text to the corresponding offset in the new text. */
  oldToNew(offset: number, bias?: Bias): number;
  /** Map an offset in the new text to the corresponding offset in the old text. */
  newToOld(offset: number, bias?: Bias): number;
  /** Map a half-open old-text range to its half-open new-text range. */
  oldRangeToNew(range: Range): Range;
  /** Map a half-open new-text range to its half-open old-text range. */
  newRangeToOld(range: Range): Range;
}
