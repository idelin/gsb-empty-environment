import { Edit, Patch, Policy } from './model';
import { validatePatch } from './apply';

/** An edit of the rebased patch that could not be preserved. */
export interface DroppedEdit {
  /** The original edit, in base-text coordinates, untouched. */
  edit: Edit;
  /** Why it was not preserved. */
  reason: 'left-priority-override' | 'conflict';
  /** Ids of the edits in the other patch whose intervals overlap it. */
  overlapsWith: string[];
}

export interface RebaseResult {
  /**
   * The rebased patch, expressed against the text AFTER `onto` has been
   * applied. Applying `onto` and then `result.patch` replays the surviving
   * edits of the source patch.
   */
  patch: Patch;
  /**
   * Every edit of the source patch that is NOT present in `patch`, with the
   * reason. No edit is ever silently lost:
   * `patch.edits.length + dropped.length === source.edits.length`.
   */
  dropped: DroppedEdit[];
  /** The subset of `dropped` reported under the 'conflict' policy. */
  conflicts: DroppedEdit[];
}

/** Half-open interval overlap: [s1, e1) x [s2, e2). */
function overlaps(s1: number, e1: number, s2: number, e2: number): boolean {
  return s1 < e2 && s2 < e1;
}

function isInsert(e: Edit): boolean {
  return e.start === e.end;
}

/**
 * Should an insert made by the `onto` patch at point p be ordered BEFORE the
 * edit `e` of the rebased patch, when both sit at the same base-text point?
 *
 *  - `e` is an insert: both are inserts at the same point -> order by
 *    (patchId, edit id), so the final text does not depend on which patch is
 *    applied first.
 *  - `e` replaces/deletes [p, q): the insert sits at the gap before `p` and
 *    lands before e's replacement text.
 *  - `e` replaces/deletes [q, p): the insert sits at the gap after `p - 1`
 *    and lands after e's replacement text.
 */
function insertPrecedes(ontoInsert: Edit, e: Edit, side: 'start' | 'end'): boolean {
  if (!isInsert(e)) {
    return side === 'start';
  }
  const a = ontoInsert.patchId + '/' + ontoInsert.id;
  const b = e.patchId + '/' + e.id;
  return a < b;
}

/**
 * Maps a base-text position `p` (one boundary of edit `e`) into the
 * coordinate system of the text after `onto` has been applied.
 *
 * Precondition: no `onto` edit overlaps `e`, so every `onto` edit is either
 * entirely before `p`, entirely after it, or an insert exactly at `p`.
 */
function mapPosition(p: number, side: 'start' | 'end', e: Edit, ontoEdits: Edit[]): number {
  let pos = p;
  for (const a of ontoEdits) {
    if (isInsert(a)) {
      if (a.start < p || (a.start === p && insertPrecedes(a, e, side))) {
        pos += a.text.length;
      }
    } else if (a.end <= p) {
      // a replaces [a.start, a.end) with a.text; positions at or after a.end
      // shift by the length delta. Positions before a.start are untouched and
      // positions strictly inside cannot occur (would be an overlap).
      pos += a.text.length - (a.end - a.start);
    }
  }
  return pos;
}

/**
 * Rebases `patch` so that it applies AFTER `onto` instead of onto the same
 * base text. Both patches must be expressed against the same base text
 * (pass `baseLength` to validate them).
 *
 * Overlap handling, per edit of `patch`:
 *  - Intervals overlap an `onto` edit (including an insert strictly inside a
 *    replaced/deleted interval):
 *      policy 'left-priority' -> the `onto` edit wins; the edit is dropped
 *                                and reported in `dropped`.
 *      policy 'conflict'      -> the edit is reported in `conflicts` (and in
 *                                `dropped`); the caller decides how to
 *                                proceed. Nothing is applied silently.
 *  - Otherwise the edit is preserved and its coordinates are shifted past
 *    the `onto` edits. Inserts at the same point are ordered by patch id.
 */
export function rebasePatch(
  patch: Patch,
  onto: Patch,
  policy: Policy,
  baseLength?: number,
): RebaseResult {
  if (baseLength !== undefined) {
    validatePatch(patch, baseLength);
    validatePatch(onto, baseLength);
  }
  const ontoEdits = [...onto.edits].sort((x, y) => x.start - y.start);
  const edits: Edit[] = [];
  const dropped: DroppedEdit[] = [];
  for (const e of [...patch.edits].sort((x, y) => x.start - y.start)) {
    const hit = ontoEdits.filter((a) => overlaps(a.start, a.end, e.start, e.end));
    if (hit.length > 0) {
      dropped.push({
        edit: e,
        reason: policy === 'left-priority' ? 'left-priority-override' : 'conflict',
        overlapsWith: hit.map((h) => h.id),
      });
      continue;
    }
    edits.push({
      ...e,
      start: mapPosition(e.start, 'start', e, ontoEdits),
      end: mapPosition(e.end, 'end', e, ontoEdits),
    });
  }
  const conflicts = dropped.filter((d) => d.reason === 'conflict');
  return { patch: { id: patch.id, edits }, dropped, conflicts };
}
