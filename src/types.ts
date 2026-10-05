export interface Block {
  oldStart: number;
  newStart: number;
  length: number;
}

export type BlockKind = "keep" | "move";

export interface ClassifiedBlock extends Block {
  kind: BlockKind;
  tokens: string[];
}

export interface TokenRange {
  start: number;
  length: number;
  tokens: string[];
}

export interface DiffOptions {
  /** Minimum length of a candidate block. Defaults to 1. */
  minBlockLength?: number;
  /** Search budget: max number of branch-and-bound states. */
  maxStates?: number;
  /** Cap on the candidate-block count before falling back to a reduced set. */
  maxCandidates?: number;
}

export interface DiffResult {
  /** Stable provenance label for every old token: `o#<oldIndex>`. */
  oldLabels: string[];
  /** Stable provenance label for every new token: `o#<oldIndex>` when
   *  matched to an old token, `n#<newIndex>` when it is an insertion. */
  newLabels: string[];
  /** oldToNew[i] = position of old token i in the new sequence, or null. */
  oldToNew: (number | null)[];
  /** newToOld[j] = source position of new token j, or null for insertions. */
  newToOld: (number | null)[];
  /** Selected non-overlapping blocks, sorted by oldStart. */
  blocks: ClassifiedBlock[];
  /** Maximal runs of old tokens not covered by any block. */
  deletions: TokenRange[];
  /** Maximal runs of new tokens not covered by any block. */
  insertions: TokenRange[];
  /** Total number of tokens preserved by the selected blocks. */
  coveredTokens: number;
  /** True when the search proved optimality within the budget. */
  optimal: boolean;
  /** Number of search states explored. */
  statesExplored: number;
}
