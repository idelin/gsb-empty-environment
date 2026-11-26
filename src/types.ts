export type RationalInput =
  | number
  | string
  | {
      numerator: number | string;
      denominator: number | string;
    }
  | {
      num: number | string;
      den: number | string;
    };

export interface FractionView {
  /** Canonical, reduced integer numerator. */
  numerator: string;
  /** Canonical, reduced positive integer denominator. */
  denominator: string;
  /** Human-readable reduced fraction, for example "2/15". */
  text: string;
}

export type PathKind = "exact" | "prefix";

export interface PathCondition {
  kind: PathKind;
  value: string;
}

export type PathInput =
  | string
  | { exact: string }
  | { prefix: string }
  | { kind: PathKind; value: string }
  | { type: PathKind; value: string }
  | { match: PathKind; path: string };

export type HeaderCondition =
  | { name: string; kind: "exact"; value: string }
  | { name: string; kind: "absent" }
  | { name: string; kind: "present" };

export interface HeaderConditionInput {
  name?: string;
  header?: string;
  key?: string;
  value?: string | null;
  exact?: string | boolean;
  absent?: boolean;
  missing?: boolean;
  present?: boolean;
  match?: "exact" | "absent" | "missing" | "present";
  type?: "exact" | "absent" | "missing" | "present";
}

export type HeadersInput =
  | readonly HeaderConditionInput[]
  | Readonly<Record<string, string | null | boolean>>;

export interface WeightedTargetInput {
  service?: string;
  cluster?: string;
  name?: string;
  destination?: string;
  weight?: RationalInput;
}

export interface DelegateTargetInput {
  table?: string;
  target?: string;
  to?: string;
  name?: string;
  weight?: RationalInput;
}

export type DelegateInput =
  | string
  | DelegateTargetInput
  | readonly (string | DelegateTargetInput)[];

export interface MirrorInput {
  service?: string;
  cluster?: string;
  name?: string;
  destination?: string;
  sampleRate?: RationalInput;
  sample?: RationalInput;
  rate?: RationalInput;
}

export interface RouteRuleInput {
  id?: string;
  path?: PathInput;
  match?: PathInput;
  headers?: HeadersInput;
  header?: HeadersInput;
  delegate?: DelegateInput;
  targets?: readonly WeightedTargetInput[];
  weightedTargets?: readonly WeightedTargetInput[];
  mirror?: MirrorInput | readonly MirrorInput[];
  mirrors?: readonly MirrorInput[];
}

export interface RouteTableInput {
  name?: string;
  rules: readonly RouteRuleInput[];
}

export interface CompileInput {
  root: RouteTableInput | readonly RouteRuleInput[];
  /** Named child route tables. Keys are the delegation IDs. */
  tables?: Readonly<Record<string, RouteTableInput | readonly RouteRuleInput[]>>;
  /** Defaults to the root table's name, then "root". */
  rootTableId?: string;
}

export interface CompileOptions {
  /** Hard guard against operator/config recursion in addition to cycle detection. */
  maxDelegateDepth?: number;
  /**
   * Keep diagnostics for rules whose entire condition is covered by earlier
   * rules. Dead rules are retained in `routes` but excluded from `liveRoutes`.
   */
  includeDeadRoutes?: boolean;
}

export interface CompiledTarget {
  service: string;
  /** Exact contribution including every enclosing delegate weight. */
  rawFraction: FractionView;
  /** Exact reduced fraction before ancestor multiplication. */
  inputFraction: FractionView;
  /** Integer obtained with the route-wide least common multiple. */
  weight: number;
  weightText: string;
}

export interface CompiledMirror {
  service: string;
  sampleRate: FractionView;
  sourceRuleIds: string[];
}

export interface RouteSourceRef {
  tableId: string;
  ruleId: string;
  index: number;
}

export interface CompiledRoute {
  id: string;
  sourceRuleIds: string[];
  sourceChain: RouteSourceRef[];
  path: PathCondition;
  headers: HeaderCondition[];
  targets: CompiledTarget[];
  /** Sum of the exact integer weights. It is the normalization scale. */
  totalWeight: number;
  totalWeightText: string;
  /** Exact product of all enclosing delegate weights. */
  branchFraction: FractionView;
  mirrors: CompiledMirror[];
  shadowedBy: string[] | null;
}

export interface DeadRuleReport {
  routeId: string;
  sourceRuleIds: string[];
  /** Expanded route IDs whose union completely covers this route. */
  shadowedBy: string[];
  /** Source rule IDs corresponding to `shadowedBy`. */
  shadowSourceRuleIds: string[];
}

export interface DelegationCycle {
  tables: string[];
  sourceRuleIds: string[];
}

export interface MirrorCycle {
  services: string[];
  sourceRuleIds: string[];
  /** Product of the aggregate sample rates around the cycle. */
  sampleGain: FractionView;
  /** True when the loop can sustain or increase mirrored traffic. */
  amplifying: boolean;
}

export type CompileIssueCode =
  | "delegate-cycle"
  | "mirror-amplification-cycle"
  | "missing-table"
  | "contradictory-path"
  | "contradictory-header"
  | "invalid-rule"
  | "invalid-weight"
  | "empty-delegation";

export interface CompileIssue {
  code: CompileIssueCode;
  severity: "error" | "warning";
  message: string;
  sourceRuleIds: string[];
  tableId?: string;
}

export interface PrunedBranch {
  sourceRuleIds: string[];
  reason: "path" | "header" | "delegate-cycle" | "missing-table";
  detail: string;
}

export interface CompiledPlan {
  rootTableId: string;
  routes: CompiledRoute[];
  liveRoutes: CompiledRoute[];
  deadRules: DeadRuleReport[];
  delegateCycles: DelegationCycle[];
  mirrorCycles: MirrorCycle[];
  prunedBranches: PrunedBranch[];
  issues: CompileIssue[];
  tableIds: string[];
}

export interface RouteRequest {
  path: string;
  headers?: Readonly<Record<string, string | readonly string[] | null | undefined>>;
}

export interface ResolvedTarget {
  service: string;
  weight: number;
  weightText: string;
  fraction: FractionView;
}

export interface CompiledResolution {
  route: CompiledRoute;
  targets: ResolvedTarget[];
  mirrors: CompiledMirror[];
}

export interface SourceResolution {
  tableId: string;
  sourceRuleIds: string[];
  targetFractions: ReadonlyArray<{
    service: string;
    fraction: FractionView;
  }>;
  mirrorServices: string[];
}
