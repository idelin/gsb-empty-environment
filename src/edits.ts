// edits.ts — primitive text edits, offset mapping and inversion.
//
// An Edit replaces text[start, end) with `inserted`. Edits passed to
// applyEdits must be sorted by start and must not overlap.

export interface Edit {
  start: number;
  end: number;
  inserted: string;
}

export interface AppliedEdit {
  start: number;
  end: number;
  inserted: string;
  removed: string;
}

export interface ApplyResult {
  text: string;
  applied: AppliedEdit[];
}

// 'start': an offset exactly at an insertion point stays before the
//          inserted text (sticky to the left).
// 'end':   an offset exactly at an insertion point moves past the
//          inserted text (sticky to the right).
export type Bias = 'start' | 'end';

function applyEdits(text: string, edits: Edit[]): ApplyResult {
  let out = '';
  let cursor = 0;
  const applied: AppliedEdit[] = [];
  for (const e of edits) {
    out += text.slice(cursor, e.start);
    out += e.inserted;
    applied.push({ start: e.start, end: e.end, inserted: e.inserted, removed: text.slice(e.start, e.end) });
    cursor = e.end;
  }
  out += text.slice(cursor);
  return { text: out, applied };
}

// Inverse edits, expressed in coordinates of the *new* text. Applying them
// restores the original byte sequence exactly.
function invertEdits(applied: AppliedEdit[]): Edit[] {
  const inverse: Edit[] = [];
  let delta = 0;
  for (const e of applied) {
    const start = e.start + delta;
    inverse.push({ start, end: start + e.inserted.length, inserted: e.removed });
    delta += e.inserted.length - (e.end - e.start);
  }
  return inverse;
}

// Map an offset in the original text through the edits into the new text.
function mapOffset(pos: number, edits: Edit[], bias: Bias): number {
  let delta = 0;
  for (const e of edits) {
    if (pos < e.start) {
      break;
    }
    if (pos <= e.end) {
      const base = e.start + delta;
      return bias === 'start' ? base : base + e.inserted.length;
    }
    delta += e.inserted.length - (e.end - e.start);
  }
  return pos + delta;
}

export { applyEdits, invertEdits, mapOffset };
