/** A dependency / conflict reference to another package. */
export interface DependencySpec {
  name: string;
  /** Range expression, e.g. ">=2 <5", "3", "*". Empty/undefined means any. */
  range?: string;
  /** Optional features to enable on the target package. */
  features?: string[];
}

/** One published version of a package. Versions are integers. */
export interface PackageVersion {
  version: number;
  dependencies?: DependencySpec[];
  /** Optional features: feature name -> extra dependencies it pulls in. */
  features?: Record<string, DependencySpec[]>;
  /** Cross-package conflicts that hold for this exact version. */
  conflicts?: DependencySpec[];
}

export interface PackageDef {
  name: string;
  versions: PackageVersion[];
}

/** A user requirement: install this package within the range, with features on. */
export interface RequestSpec {
  name: string;
  range?: string;
  features?: string[];
}

export interface SolverInput {
  repository: PackageDef[];
  /** Currently installed state: {name: version} or a list of {name, version}. */
  installed?: Record<string, number> | Array<{ name: string; version: number }>;
  request: RequestSpec[];
}

export type ChangeKind = "keep" | "upgrade" | "downgrade" | "new";

export interface PlanEntry {
  name: string;
  version: number;
  /** Features enabled on this package in the final plan. */
  features: string[];
  change: ChangeKind;
}

export interface PlanOk {
  status: "ok";
  plan: PlanEntry[];
  /** Installed packages that are no longer part of the plan. */
  removed: string[];
  /** Number of packages whose final state differs from the installed state. */
  changes: number;
}

export interface PlanConflict {
  status: "conflict";
  /**
   * A minimal (irreducible) subset of the request constraints that is already
   * unsatisfiable together with the repository facts. Removing any element
   * makes it satisfiable, so the conflict can be reviewed/verified by hand.
   */
  core: RequestSpec[];
  /** Human-readable facts needed to review the conflict. */
  explanation: string[];
}

export type SolveResult = PlanOk | PlanConflict;
