/**
 * Core model for the patch-rebase tool.
 *
 * A patch is a set of edits expressed against ONE common base text version.
 * An edit replaces the half-open interval [start, end) of the base text with
 * `text`:
 *   - start < end, text !== ""  -> replace
 *   - start < end, text === ""  -> delete
 *   - start === end             -> insert (empty-text inserts are no-ops and
 *                                  are rejected by validation)
 *
 * Edits inside one patch must be sorted by `start` and non-overlapping
 * (previous.end <= next.start). Several inserts at the same point are
 * allowed; their relative order is the order they appear in `edits`.
 */

/** How to resolve two edits whose base-text intervals overlap. */
export type Policy =
  /** The patch being rebased onto ("left") wins; the overlapping edit of the
   *  rebased patch is dropped and reported in `dropped`. */
  | 'left-priority'
  /** The overlap is reported as an unresolved conflict in `conflicts`. */
  | 'conflict';

export interface Edit {
  /** Unique id of the edit inside its patch (used for provenance and as a
   *  tie-breaker for same-patch same-point inserts). */
  id: string;
  /** Id of the patch this edit belongs to. */
  patchId: string;
  /** Start offset in the base text, inclusive. */
  start: number;
  /** End offset in the base text, exclusive. */
  end: number;
  /** Replacement text. */
  text: string;
}

export interface Patch {
  id: string;
  edits: Edit[];
}

/** Origin of one character of an applied result. */
export type Provenance =
  | { kind: 'base'; index: number }
  | { kind: 'edit'; patchId: string; editId: string; offset: number };

export interface AppliedText {
  text: string;
  /** provenance[i] describes the origin of text[i]. */
  provenance: Provenance[];
}
