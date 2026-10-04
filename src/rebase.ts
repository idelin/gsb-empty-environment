import type { Edit, Patch } from './model.ts';
import { checkPatchShape, isInsertion } from './model.ts';

/**
 * - 'left-priority': on overlap the left patch (the one being rebased onto)
 *   wins; the right edit is moved to `unpreserved`.
 * - 'conflict': on overlap the edit is reported in `conflicts` (and also
 *   listed in `unpreserved`, since it cannot be kept). Callers should treat
 *   a non-empty `conflicts` list as a failed rebase.
 */
export type Strategy = 'left-priority' | 'conflict';

export interface Origin {
  patchId: string;
  editId: string;
}

export interface RebasedEdit extends Edit {
  /** Provenance: which original edit this rebased edit derives from. */
  origin: Origin;
}

export interface RebasedPatch {
  id: string;
  edits: RebasedEdit[];
}

export interface UnpreservedEdit {
  /** The original edit (base-text coordinates) that could not be kept. */
  edit: Edit;
  reason: string;
}

export interface EditConflict {
  left: Edit;
  right: Edit;
}

export interface RebaseResult {
  /** The right patch, transformed to apply after the left patch. */
  patch: RebasedPatch;
  /** Original right-patch edits that could not be preserved. Never silent. */
  unpreserved: UnpreservedEdit[];
  /** Overlapping edit pairs; only populated with the 'conflict' strategy. */
  conflicts: EditConflict[];
}

function compareIds(
  leftPatchId: string,
  leftEditId: string,
  rightPatchId: string,
  rightEditId: string,
): number {
  if (leftPatchId !== rightPatchId) return leftPatchId < rightPatchId ? -1 : 1;
  if (leftEditId !== rightEditId) return leftEditId < rightEditId ? -1 : 1;
  return 0;
}

/**
 * Two edits conflict when their base-text intervals strictly overlap.
 * Touching boundaries do not conflict; an insertion conflicts only with an
 * edit whose range strictly contains the insertion point. Two insertions at
 * the same point never conflict: they are ordered by patch id.
 */
export function editsConflict(a: Edit, b: Edit): boolean {
  if (isInsertion(a) && isInsertion(b)) return false;
  if (isInsertion(a)) return b.start < a.start && a.start < b.end;
  if (isInsertion(b)) return a.start < b.start && b.start < a.end;
  return a.start < b.end && b.start < a.end;
}

/**
 * Maps one right-patch edit into post-left coordinates.
 *
 * Boundary conventions (chosen so that both application orders converge):
 * - a left range edit contributes its delta to every base position at or
 *   after its end;
 * - a left insertion at the start boundary of a right range edit maps before
 *   that edit (the right edit's start shifts past it);
 * - a left insertion at the end boundary of a right range edit maps after
 *   that edit;
 * - a left insertion at the same point as a right insertion is ordered by
 *   (patchId, editId): the smaller id's text comes first.
 */
function rebaseEdit(left: Patch, right: Patch, edit: Edit): RebasedEdit {
  let start = edit.start;
  let end = edit.end;
  for (const a of left.edits) {
    const delta = a.text.length - (a.end - a.start);
    if (isInsertion(a)) {
      const point = a.start;
      const leftFirst =
        compareIds(left.id, a.id, right.id, edit.id) < 0;
      if (point < edit.start) start += delta;
      else if (point === edit.start && (!isInsertion(edit) || leftFirst)) start += delta;
      if (point < edit.end) end += delta;
      else if (point === edit.end && isInsertion(edit) && leftFirst) end += delta;
    } else {
      if (a.end <= edit.start) start += delta;
      if (a.end <= edit.end) end += delta;
    }
  }
  return {
    id: edit.id,
    start,
    end,
    text: edit.text,
    origin: { patchId: right.id, editId: edit.id },
  };
}

/**
 * Rebases `right` so it applies to the result of applying `left` to the
 * shared base text. Both patches must target the same base version.
 *
 * Conservation guarantee: every right edit ends up either exactly once in
 * `result.patch.edits` (with an `origin` back-reference) or exactly once in
 * `result.unpreserved`. Nothing is dropped silently.
 */
export function rebase(left: Patch, right: Patch, strategy: Strategy): RebaseResult {
  checkPatchShape(left);
  checkPatchShape(right);
  const result: RebaseResult = {
    patch: { id: right.id, edits: [] },
    unpreserved: [],
    conflicts: [],
  };
  for (const edit of right.edits) {
    const hit = left.edits.find((a) => editsConflict(a, edit));
    if (hit !== undefined) {
      const reason =
        strategy === 'conflict'
          ? `conflicts with ${left.id}:${hit.id}`
          : `overlaps ${left.id}:${hit.id}; left patch takes priority`;
      if (strategy === 'conflict') {
        result.conflicts.push({ left: hit, right: edit });
      }
      result.unpreserved.push({ edit, reason });
      continue;
    }
    result.patch.edits.push(rebaseEdit(left, right, edit));
  }
  return result;
}
