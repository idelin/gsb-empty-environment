/** Three-valued outcome: 'unknown' when a sealed attribute blocks evaluation. */
export type Outcome = 'true' | 'false' | 'unknown';

export interface ReadRecord {
  attr: string;
  /** Concrete integer, or 'sealed' when only a placeholder was available. */
  value: number | 'sealed';
}

/**
 * One node of the evaluation trace. Children are referenced by the structural
 * hash of their sub-rule, so identical sub-expressions share a single subproof
 * (DAG compression). `children` preserves actual evaluation order; `skipped`
 * lists branches never evaluated due to short-circuiting.
 */
export interface TraceNode {
  ruleHash: string;
  outcome: Outcome;
  reads: ReadRecord[];
  children: string[];
  skipped: string[];
}

export interface EvaluationTrace {
  /** Hash of the root subproof. */
  root: string;
  /** Shared subproofs, keyed by sub-rule hash. Each stored exactly once. */
  subproofs: Record<string, TraceNode>;
  /** Flattened actual read set, in the order the reads happened. */
  reads: ReadRecord[];
}
