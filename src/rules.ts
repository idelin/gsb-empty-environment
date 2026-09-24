/**
 * Public, deterministic rules for edits and composition ranges.
 *
 * An `Edit` replaces the UTF-16 range [start, end) with `text`
 * (pure insertion when start === end, pure deletion when text === "").
 *
 * Conflict rule (composition range C = [cs, ce), edit E = [s, s+len)):
 *   - A deletion/replacement conflicts iff it overlaps C: s < ce && e > cs.
 *   - A pure insertion conflicts iff it lands STRICTLY inside C: cs < s < ce.
 *   - Insertions exactly at cs or ce do NOT conflict; they transform C:
 *       * at/before cs  -> the remote text goes BEFORE the composition
 *         (the whole range shifts right by the edit delta);
 *       * at/after ce   -> the remote text goes AFTER the composition
 *         (the range is unchanged).
 *   - A conflicting edit ends the composition (default policy: cancel).
 *   - A non-conflicting edit transforms the composition range and the
 *     composition continues.
 *
 * All offsets are snapped so surrogate pairs are never split.
 */

import { snapRange } from './utf16.ts';

export interface Edit {
  start: number;
  end: number;
  text: string;
}

export interface Range {
  start: number;
  end: number;
}

export function editLengthOf(text: string): Edit {
  return { start: 0, end: 0, text };
}

/** Deterministic conflict predicate (see module docs). */
export function editsConflict(comp: Range, edit: Edit): boolean {
  if (edit.start === edit.end) {
    return comp.start < edit.start && edit.start < comp.end;
  }
  return edit.start < comp.end && edit.end > comp.start;
}

/**
 * Positional transform of a range through a NON-conflicting edit.
 * Precondition: `!editsConflict(range, edit)`.
 */
export function transformRange(range: Range, edit: Edit): Range {
  const delta = edit.text.length - (edit.end - edit.start);
  if (edit.end <= range.start) {
    return { start: range.start + delta, end: range.end + delta };
  }
  return { start: range.start, end: range.end };
}

/**
 * Transform a single point through an edit.
 * Points inside the replaced span map to the end of the inserted text.
 */
export function transformPoint(point: number, edit: Edit): number {
  if (point <= edit.start) return point;
  if (point >= edit.end) {
    return point + (edit.text.length - (edit.end - edit.start));
  }
  return edit.start + edit.text.length;
}

/**
 * Apply an edit to a text. Offsets are clamped and snapped so a surrogate
 * pair is never split; the result never introduces lone surrogates when the
 * inputs are well-formed.
 */
export function applyEdit(text: string, edit: Edit): string {
  const { start, end } = snapRange(text, edit.start, edit.end);
  return text.slice(0, start) + edit.text + text.slice(end);
}

/** Replace `range` of `text` with `inserted` (used for projections). */
export function spliceRange(text: string, range: Range, inserted: string): string {
  return text.slice(0, range.start) + inserted + text.slice(range.end);
}
