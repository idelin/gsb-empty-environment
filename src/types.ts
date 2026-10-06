/** A single candidate offered by one async source for the current query. */
export interface Candidate {
  /** Source-local identifier, kept for debugging/tracing. */
  readonly id: string;
  /** Raw display key; dedupe happens on its normalized form. */
  readonly key: string;
  /** Relevance score assigned by the source. */
  readonly score: number;
  /** Optional human-readable reason this candidate was suggested. */
  readonly explanation?: string;
}

/**
 * A merged suggestion: all contributions that share a normalized key,
 * aggregated into one entry.
 */
export interface Suggestion {
  /** Normalized dedupe key. */
  readonly key: string;
  /** Winning score: the maximum across all contributing sources. */
  readonly score: number;
  /** Set of source ids that contributed to this key. */
  readonly sources: ReadonlySet<string>;
  /** Per-source explanations, keyed by source id. */
  readonly explanations: ReadonlyMap<string, string>;
}

/** Maps a raw candidate key to its canonical dedupe form. */
export type NormalizeFn = (key: string) => string;
