import { buildPositionMap } from './positionMap';
import { expandReplacement } from './replacement';
import { PositionMap, TextEdit } from './types';

export interface MatchInfo {
  /** Half-open range of the match in the old (frozen) text. */
  start: number;
  end: number;
  /** The matched text (empty string for zero-width matches). */
  text: string;
  /** The expanded replacement text for this match. */
  replacement: string;
}

export interface ReplaceResult {
  /** The new text after all matches were replaced simultaneously. */
  text: string;
  /** All matches, in document order, with their expanded replacements. */
  matches: MatchInfo[];
  /** The simultaneous edits that were applied (one per match). */
  edits: TextEdit[];
  /** Bidirectional offset mapping between the old and new text. */
  map: PositionMap;
}

/**
 * Find every non-overlapping match of `pattern` in the frozen old `text`.
 *
 * Scanning is leftmost, non-overlapping (regex `lastIndex` semantics).
 * A zero-width match consumes its boundary: after an empty match at offset
 * k the scan resumes at k + 1, so each boundary matches at most once.
 */
export function findMatches(text: string, pattern: RegExp): RegExpExecArray[] {
  const flags = pattern.flags.includes('g') ? pattern.flags : pattern.flags + 'g';
  const re = new RegExp(pattern.source, flags);
  const matches: RegExpExecArray[] = [];
  let m: RegExpExecArray | null;
  while ((m = re.exec(text)) !== null) {
    matches.push(m);
    if (m[0].length === 0) {
      re.lastIndex += 1;
    }
  }
  return matches;
}

/**
 * Replace all matches of `pattern` in `text` with `replacement`.
 *
 * All matches are determined against the frozen old text first, then applied
 * simultaneously: matches that the replacement text itself would create are
 * NOT processed in this round, and replacements never see each other's output.
 *
 * `replacement` is either a template string (see expandReplacement) or a
 * function receiving the match and returning the replacement text.
 */
export function replaceAll(
  text: string,
  pattern: RegExp,
  replacement: string | ((match: RegExpExecArray) => string),
): ReplaceResult {
  const raw = findMatches(text, pattern);
  const edits: TextEdit[] = [];
  const matches: MatchInfo[] = [];
  for (const m of raw) {
    const rep = typeof replacement === 'function' ? replacement(m) : expandReplacement(replacement, m, text);
    edits.push({ start: m.index, end: m.index + m[0].length, newText: rep });
    matches.push({ start: m.index, end: m.index + m[0].length, text: m[0], replacement: rep });
  }
  const newText = applyEdits(text, edits);
  return { text: newText, matches, edits, map: buildPositionMap(text.length, edits) };
}

/**
 * Apply non-overlapping edits simultaneously against the frozen old `text`
 * using naive concatenation: unchanged prefixes/suffixes and replacement
 * texts are spliced together in document order.
 */
export function applyEdits(text: string, edits: TextEdit[]): string {
  const sorted = [...edits].sort((a, b) => a.start - b.start || a.end - b.end);
  let out = '';
  let cursor = 0;
  for (const edit of sorted) {
    if (edit.start < cursor) {
      throw new Error(`overlapping edits at offset ${edit.start}`);
    }
    if (edit.start < 0 || edit.end > text.length || edit.start > edit.end) {
      throw new RangeError(`invalid edit range [${edit.start}, ${edit.end})`);
    }
    out += text.slice(cursor, edit.start) + edit.newText;
    cursor = edit.end;
  }
  return out + text.slice(cursor);
}

/** Compute the inverse of a set of simultaneous edits (for undo). */
export function invertEdits(
  oldText: string,
  edits: TextEdit[],
  map: PositionMap,
): { start: number; end: number; oldText: string }[] {
  const sorted = [...edits].sort((a, b) => a.start - b.start || a.end - b.end);
  return sorted.map((edit) => {
    const range = map.oldRangeToNew({ start: edit.start, end: edit.end });
    return { start: range.start, end: range.end, oldText: oldText.slice(edit.start, edit.end) };
  });
}
