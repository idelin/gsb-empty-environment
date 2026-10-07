/**
 * Edit primitives. All offsets are UTF-16 code-unit indices into the
 * document string. An Edit replaces `removedLen` code units starting at
 * `offset` with the string `inserted`.
 */

export interface Edit {
  offset: number;
  removedLen: number;
  inserted: string;
}

export interface Selection {
  anchor: number;
  head: number;
}

/** Apply non-overlapping edits (any order) to `text`. */
export function applyEdits(text: string, edits: readonly Edit[]): string {
  const sorted = [...edits].sort((a, b) => b.offset - a.offset);
  let out = text;
  for (const e of sorted) {
    out = out.slice(0, e.offset) + e.inserted + out.slice(e.offset + e.removedLen);
  }
  return out;
}

/**
 * Compute the inverse of `edits` against the original `text`. The
 * returned edits are expressed in edited-text coordinates, so applying
 * them with `applyEdits` to the edited text restores `text`
 * byte-for-byte.
 */
export function invertEdits(text: string, edits: readonly Edit[]): Edit[] {
  const ascending = [...edits].sort((a, b) => a.offset - b.offset);
  let delta = 0;
  return ascending.map((e) => {
    const inverse: Edit = {
      offset: e.offset + delta,
      removedLen: e.inserted.length,
      inserted: text.slice(e.offset, e.offset + e.removedLen),
    };
    delta += e.inserted.length - e.removedLen;
    return inverse;
  });
}

/**
 * Map a position through `edits` (given in original-text coordinates).
 * - Positions before an edit are unchanged.
 * - A position exactly at a pure insertion point moves forward with the
 *   inserted text (so cursors ride along with inserted indentation).
 * - A position inside a removed region clamps to the region start.
 * - Positions after an edit shift by the edit's length delta.
 */
export function mapPosition(pos: number, edits: readonly Edit[]): number {
  let delta = 0;
  const sorted = [...edits].sort((a, b) => a.offset - b.offset);
  for (const e of sorted) {
    if (pos < e.offset) break;
    if (pos > e.offset + e.removedLen) {
      delta += e.inserted.length - e.removedLen;
    } else if (e.removedLen === 0) {
      delta += e.inserted.length;
    } else {
      return e.offset + delta;
    }
  }
  return pos + delta;
}

export function mapSelection(sel: Selection, edits: readonly Edit[]): Selection {
  return { anchor: mapPosition(sel.anchor, edits), head: mapPosition(sel.head, edits) };
}

export function mapSelections(sels: readonly Selection[], edits: readonly Edit[]): Selection[] {
  return sels.map((s) => mapSelection(s, edits));
}
