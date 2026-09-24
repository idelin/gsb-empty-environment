/** Half-open range of UTF-16 code-unit offsets: [start, end). */
export type Range = { start: number; end: number };

/**
 * An edit applied to a document. Edits inside one commit are applied
 * sequentially; each edit is expressed in the coordinates produced by the
 * previous edits of the same commit.
 *
 * `move.to` is the insertion offset in the document *after* the moved block
 * has been removed. A `move` only preserves the identity of enclosed
 * diagnostics when it carries a stable `moveId`; without one it behaves
 * exactly like delete + insert.
 */
export type Edit =
  | { kind: 'insert'; at: number; text: string }
  | { kind: 'delete'; start: number; end: number }
  | { kind: 'replace'; start: number; end: number; text: string }
  | { kind: 'move'; start: number; end: number; to: number; moveId?: string };

/** One recorded step of evidence explaining how a range was transformed. */
export type Evidence = {
  version: { from: number; to: number };
  editIndex: number;
  kind: 'shift' | 'expand' | 'shrink' | 'move-follow' | 'deleted';
  detail: string;
};

/** Context captured around a diagnostic's range at bind time. */
export type ContextSummary = {
  /** Up to `radius` UTF-16 units before the range (code-point aligned). */
  prefix: string;
  /** Up to `radius` UTF-16 units after the range (code-point aligned). */
  suffix: string;
  /** Exact text covered by the range at bind time. */
  target: string;
};

export type Diagnostic = {
  id: string;
  markerId: string;
  /** Version the diagnostic was bound to. */
  bindVersion: number;
  /** UTF-16 range at `bindVersion`. */
  range: Range;
  context: ContextSummary;
  message: string;
};

export type Candidate = {
  range: Range;
  /** Why this candidate was produced. */
  evidence: string;
  /** Context match score (matched prefix + suffix units, line-ending insensitive). */
  contextScore: number;
};

export type ExcludedCandidate = {
  range: Range;
  /** Why this occurrence was rejected. */
  reason: string;
};

export type Relocation =
  | {
      status: 'certain';
      range: Range;
      /**
       * `identity`: followed through recorded edits (strongest evidence).
       * `text-similarity`: recovered by searching text + context (weaker;
       * never reported when duplicates make the match ambiguous).
       */
      basis: 'identity' | 'text-similarity';
      evidence: Evidence[] | string[];
      excluded: ExcludedCandidate[];
    }
  | {
      status: 'ambiguous';
      candidates: Candidate[];
      excluded: ExcludedCandidate[];
      evidence: string[];
    }
  | {
      status: 'invalid';
      reason: string;
      evidence: Evidence[] | string[];
    };
