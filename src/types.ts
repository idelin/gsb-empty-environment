/**
 * A half-open range of character offsets: [start, end).
 * An empty range (start === end) denotes an insertion point.
 */
export interface Range {
  readonly start: number;
  readonly end: number;
}

/**
 * A single document edit: replace the half-open span [start, end)
 * (expressed against the pre-edit document) with `text`.
 * A pure insertion has start === end; a pure deletion has text === "".
 */
export interface TextEdit {
  readonly start: number;
  readonly end: number;
  readonly text: string;
}

/**
 * How a boundary position behaves when text is inserted exactly at it:
 * - "before": the position stays in front of the inserted text (insertion lands outside).
 * - "after":  the position moves past the inserted text (insertion is absorbed).
 */
export type BoundaryAffinity = "before" | "after";

/** Affinity of the two boundaries of a mapped range. */
export interface RangeAffinity {
  readonly start: BoundaryAffinity;
  readonly end: BoundaryAffinity;
}

/**
 * Default affinity for completion replacement ranges: typing at either
 * boundary absorbs the new text into the range, so continued typing at the
 * cursor (the range end) grows the replacement range — matching how
 * completion UIs re-filter as you type.
 */
export const DEFAULT_COMPLETION_AFFINITY: RangeAffinity = {
  start: "after",
  end: "after",
};

export function range(start: number, end: number): Range {
  if (!Number.isInteger(start) || !Number.isInteger(end) || start < 0 || end < start) {
    throw new RangeError(`invalid range [${start}, ${end})`);
  }
  return { start, end };
}

export function edit(start: number, end: number, text: string): TextEdit {
  if (!Number.isInteger(start) || !Number.isInteger(end) || start < 0 || end < start) {
    throw new RangeError(`invalid edit span [${start}, ${end})`);
  }
  return { start, end, text };
}
