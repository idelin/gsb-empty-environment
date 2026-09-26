export type Json =
  | null
  | boolean
  | number
  | string
  | Json[]
  | { [key: string]: Json };

export type SchemaObject = { [keyword: string]: unknown };
export type Schema = boolean | SchemaObject;

export interface ValidationError {
  /** JSON pointer into the instance, e.g. "#/contact/email". */
  instancePath: string;
  /** JSON pointer into the schema at the failing rule, e.g. "#/oneOf/0/type". */
  schemaPath: string;
  keyword: string;
  message: string;
  params?: Record<string, unknown>;
  /**
   * Extra rule locations (in sibling branches) where the identical rule
   * failed on the same instance location. Present only on deduplicated
   * errors inside a combinator diagnosis.
   */
  alsoAt?: string[];
  /** Present on oneOf/anyOf errors. */
  diagnosis?: CombinatorDiagnosis;
}

export interface BranchReport {
  index: number;
  schemaPath: string;
  ok: boolean;
  /** The branch's own errors, with their own paths preserved. */
  errors: ValidationError[];
}

export interface CoverInfo {
  /** true when the selected set is proven minimal. */
  optimal: boolean;
  /** Proven lower bound on the optimum (valid even when not optimal). */
  lowerBound: number;
  /** Size of the selected set (upper bound on the optimum). */
  upperBound: number;
  /** How many duplicate error occurrences were removed by rule identity. */
  sharedErrors: number;
}

export interface CombinatorDiagnosis {
  kind: 'oneOf' | 'anyOf';
  reason: 'no-branch-matched' | 'multiple-branches-matched';
  branches: BranchReport[];
  /** Indices of branches that validated successfully. */
  matched: number[];
  /**
   * Minimal error set explaining why every branch fails
   * (only for reason "no-branch-matched").
   */
  selectedErrors: ValidationError[];
  /** Ambiguity witness for oneOf matching more than one branch. */
  witness?: { instancePath: string; matchedBranches: number[] };
  cover?: CoverInfo;
  /**
   * false when the error budget truncated evidence that is the sole
   * refutation of some branch; the diagnosis then must not be treated
   * as a complete explanation.
   */
  complete: boolean;
  /** Failed branches left without any selected evidence (when !complete). */
  uncoveredBranches?: number[];
}

export interface ValidateOptions {
  /** Cap on selectedErrors per combinator diagnosis. Default: Infinity. */
  maxErrors?: number;
  /** Search budget (nodes) for the exact minimal-cover solver. */
  coverMaxNodes?: number;
}

export interface Verification {
  sufficient: boolean;
  problems: string[];
}
