export interface Edit {
  /** Stable id, unique within its patch. */
  id: string;
  /** Start offset in the base text, inclusive. */
  start: number;
  /** End offset in the base text, exclusive. start === end means insertion. */
  end: number;
  /** Replacement text for [start, end). */
  text: string;
}

export interface Patch {
  id: string;
  /**
   * Edits sorted by (start, end) and non-overlapping. Multiple insertions at
   * the same offset are allowed and apply in array order.
   */
  edits: Edit[];
}

export function isInsertion(edit: Edit): boolean {
  return edit.start === edit.end;
}

/** Validates ordering and disjointness independent of any base text. */
export function checkPatchShape(patch: Patch): void {
  let previous: Edit | null = null;
  for (const edit of patch.edits) {
    if (
      !Number.isInteger(edit.start) ||
      !Number.isInteger(edit.end) ||
      edit.start < 0 ||
      edit.end < edit.start
    ) {
      throw new Error(
        `patch ${patch.id}: edit ${edit.id} has invalid range [${edit.start}, ${edit.end})`,
      );
    }
    if (previous !== null) {
      if (edit.start < previous.end) {
        throw new Error(
          `patch ${patch.id}: edits ${previous.id} and ${edit.id} overlap or are unsorted`,
        );
      }
    }
    previous = edit;
  }
}

export function validatePatch(base: string, patch: Patch): void {
  checkPatchShape(patch);
  for (const edit of patch.edits) {
    if (edit.end > base.length) {
      throw new Error(
        `patch ${patch.id}: edit ${edit.id} ends at ${edit.end}, beyond base length ${base.length}`,
      );
    }
  }
}

export function applyPatch(base: string, patch: Patch): string {
  validatePatch(base, patch);
  let output = '';
  let cursor = 0;
  for (const edit of patch.edits) {
    output += base.slice(cursor, edit.start) + edit.text;
    cursor = edit.end;
  }
  return output + base.slice(cursor);
}
