/** A token is an opaque string; callers may pre-tokenize text however they like. */
export type Token = string;

export type Equality = (a: Token, b: Token) => boolean;

export interface DiffOptions {
  /**
   * Search budget: maximum number of DP state expansions spent optimizing the
   * move-block plan. When the budget is exhausted, a greedy (still valid and
   * verifiable, but possibly suboptimal) plan is returned instead.
   * Defaults to Infinity.
   */
  budget?: number;
  /** Token equality, defaults to strict string equality. */
  equals?: Equality;
}

export type OldStatus = 'kept' | 'moved' | 'deleted';
export type NewStatus = 'kept' | 'moved' | 'inserted';

export interface LabeledOldToken {
  text: Token;
  /** Position in the old token sequence. */
  index: number;
  /** Stable origin label: "O<oldIndex>". */
  label: string;
  status: OldStatus;
  /** Move-block id when status === 'moved', otherwise null. */
  blockId: number | null;
}

export interface LabeledNewToken {
  text: Token;
  /** Position in the new token sequence. */
  index: number;
  /** "O<oldIndex>" when carried over from the old sequence, "I<n>" for insertions. */
  label: string;
  status: NewStatus;
  /** Move-block id when status === 'moved', otherwise null. */
  blockId: number | null;
}

export interface MoveBlock {
  id: number;
  /** Inclusive span in the old sequence; may cover deleted tokens (edits inside the block). */
  oldStart: number;
  oldEnd: number;
  /** Inclusive span in the new sequence; may cover inserted tokens (edits inside the block). */
  newStart: number;
  newEnd: number;
  /** Number of matched (carried-over) tokens in the block. */
  size: number;
}

export interface DiffStats {
  kept: number;
  moved: number;
  deleted: number;
  inserted: number;
  blockCount: number;
  /** Sum of source (old) indices of moved tokens; the final tie-break key. */
  movedSourceSum: number;
  /** False when the budget was exhausted and the greedy fallback was used. */
  optimal: boolean;
  budgetExhausted: boolean;
  /** Number of DP state expansions actually explored. */
  nodesExplored: number;
}

export interface DiffResult {
  oldTokens: LabeledOldToken[];
  newTokens: LabeledNewToken[];
  blocks: MoveBlock[];
  stats: DiffStats;
}
