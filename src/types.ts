export type ConditionValue =
  | string
  | null
  | ConditionObject
  | ConditionValue[];

export interface ConditionObject {
  [key: string]: ConditionValue;
}

export type ExportsField =
  | string
  | null
  | ConditionObject
  | ConditionValue[];

export type TraceOutcome = "target" | "blocked" | "nested-no-match";

export interface TraceAttempt {
  keyPath: string[];
  active: boolean;
  outcome: TraceOutcome;
  target: string | null;
}

export interface Trace {
  attempts: TraceAttempt[];
  winningKeyPath: string[] | null;
  blockedKeyPath: string[] | null;
}

export interface ResolveResult {
  found: boolean;
  blocked: boolean;
  target: string | null;
  trace: Trace;
}

export type ReviewedTable = Record<string, string>;

export interface GateInput {
  packageName?: string;
  specifier: string;
  exports: ExportsField;
  baselineConditions: readonly string[];
  additionalConditions?: readonly string[];
  allowedConditions?: readonly string[];
  manifest: readonly string[];
  reviewed?: ReviewedTable;
  readFile?: (relPath: string) => Uint8Array;
}

export type GateReason =
  | "allow-same-entry"
  | "allow-reviewed-entry"
  | "deny-unresolvable"
  | "deny-blocked"
  | "deny-missing-from-manifest"
  | "deny-unreviewed-entry"
  | "deny-hash-mismatch"
  | "deny-hash-unreadable";

export interface ProbeResult {
  condition: string;
  target: string | null;
  blocked: boolean;
  diverges: boolean;
}

export interface ReviewVerdict {
  listed: boolean;
  expected?: string;
  actual?: string;
  hashOk: boolean | null;
  readError?: string;
}

export interface GateResult {
  decision: "allow" | "deny";
  reason: GateReason;
  specifier: string;
  subpath: string;
  selectedEntry: string | null;
  activeAdditionalConditions: string[];
  ignoredConditions: string[];
  baseline: ResolveResult;
  augmented: ResolveResult;
  divergence: {
    changed: boolean;
    condition: string | null;
    attribution: "single" | "combined" | null;
    key: string[] | null;
    baselineKey: string[] | null;
    probes: ProbeResult[];
  };
  manifest: {
    selectedListed: boolean;
    baselineListed: boolean;
  };
  review: ReviewVerdict;
}
