// Core domain types for the policy conflict analyzer.
//
// A policy may only read explicitly declared attributes (the schema).
// Supported condition language: finite-set membership, integer comparisons,
// and boolean combinations. No external identity service is consulted.

export type AttrSchema =
  | { kind: 'enum'; values: readonly string[] }
  | { kind: 'int'; min: number; max: number };

export type Schema = Record<string, AttrSchema>;

export type CmpOp = '<' | '<=' | '==' | '>=' | '>' | '!=';

export type Cond =
  | { op: 'const'; value: boolean }
  | { op: 'in'; attr: string; values: readonly string[] }
  | { op: 'cmp'; attr: string; cmp: CmpOp; value: number }
  | { op: 'and'; args: readonly Cond[] }
  | { op: 'or'; args: readonly Cond[] }
  | { op: 'not'; arg: Cond };

export type Effect = 'allow' | 'deny';

export interface Rule {
  id: string;
  effect: Effect;
  when: Cond;
}

export type Point = Record<string, number | string>;

export interface Budget {
  /** Max disjoint regions kept during normalization before the search is cut off. */
  maxRegions?: number;
  /** Max input points the independent enumeration cross-check may visit. */
  maxEnum?: number;
}

export interface RegionReport {
  regionId: number;
  /** Human-readable per-attribute constraint; '*' means unconstrained. */
  description: Record<string, string>;
  allowRules: string[];
  denyRules: string[];
  /** Minimal counterexample instance inside this region (lexicographically smallest point). */
  witness: Point;
}

export interface VerificationReport {
  mode: 'exhaustive' | 'partial';
  checked: number;
  total: number;
  /** True when the enumeration did not cover the whole domain (budget too small). */
  pending: boolean;
  mismatches: string[];
}

export interface Report {
  /** 'partial' means the search was cut off by budget: unvisited areas are undecided,
   *  never silently claimed conflict-free. */
  status: 'complete' | 'partial';
  pendingReason?: string;
  regions: RegionReport[];
  /** Minimal conflicting rule sets: allow/deny pairs whose conditions overlap. */
  minimalConflictSets: Array<{ allow: string; deny: string }>;
  rulesInvolved: string[];
  verification: VerificationReport;
  stats: {
    dnfCacheHits: number;
    dnfCacheMisses: number;
    pairComputations: number;
  };
}
