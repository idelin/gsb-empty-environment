import { Candidate } from "./replacer";

/** Half-open span edit against the ORIGINAL coordinates. */
export interface Edit {
  start: number;
  end: number;
  text: string;
}

export function editsFromCandidates(candidates: Candidate[]): Edit[] {
  return candidates.map((c) => ({ start: c.start, end: c.end, text: c.replacement }));
}

/** Apply non-overlapping edits (coordinates of `source`) to a string. */
export function applyEdits(source: string, edits: Edit[]): string {
  const ordered = [...edits].sort((a, b) => a.start - b.start);
  let out = "";
  let cursor = 0;
  for (const edit of ordered) {
    out += source.slice(cursor, edit.start) + edit.text;
    cursor = edit.end;
  }
  return out + source.slice(cursor);
}

/**
 * Build the inverse patch: edits expressed in the NEW text's coordinates
 * that restore the original text. Forward edits are non-overlapping and
 * sorted, so each inverse span is the original start shifted by the
 * cumulative length delta of all previous edits.
 */
export function invertEdits(source: string, edits: Edit[]): Edit[] {
  const ordered = [...edits].sort((a, b) => a.start - b.start);
  const inverse: Edit[] = [];
  let delta = 0;
  for (const edit of ordered) {
    const start = edit.start + delta;
    inverse.push({
      start,
      end: start + edit.text.length,
      text: source.slice(edit.start, edit.end),
    });
    delta += edit.text.length - (edit.end - edit.start);
  }
  return inverse;
}
