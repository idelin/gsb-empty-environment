/**
 * Domain model for the org-permission diff tool.
 *
 * Identity rules:
 * - Org nodes are matched across snapshots by `id`. Renaming a node changes
 *   only `name`, therefore it never produces a permission diff.
 * - Users are matched by `userId`; their org placement may change between
 *   the old and the new tree (that is the migration being measured).
 * - Policies and exceptions carry stable `id`s so a deleted exception can be
 *   detected and its (removed) influence recomputed.
 */

export type Effect = "allow" | "deny";

export interface OrgNode {
  /** Stable identity. Never reused across different orgs. */
  id: string;
  /** Display name. May change (rename) without affecting permissions. */
  name: string;
  parentId: string | null;
}

export interface OrgMembership {
  userId: string;
  nodeId: string;
}

export interface OrgTree {
  nodes: OrgNode[];
  members: OrgMembership[];
}

/** Permission rule attached to an org node; inherited by all descendants. */
export interface OrgPolicy {
  id: string;
  nodeId: string;
  effect: Effect;
  resourceType: string;
  action: string;
  /** "a/b/c" (exact), "a/b/*" (prefix, includes "a/b" itself), or "*". */
  pathPattern: string;
}

/** Explicit per-user rule. Always wins over inherited org policies. */
export interface UserException {
  id: string;
  userId: string;
  effect: Effect;
  resourceType: string;
  action: string;
  pathPattern: string;
}

/** One snapshot of the authorization state (before or after migration). */
export interface Snapshot {
  tree: OrgTree;
  policies: OrgPolicy[];
  exceptions: UserException[];
}

/** A concrete resource whose access is being audited. */
export interface Resource {
  type: string;
  path: string;
}

/** How a permission (or its loss) is explained: the shortest source chain. */
export interface SourceChain {
  /** Human-readable hops from the rule to the user, shortest possible. */
  hops: string[];
  /** Stable identity of the winning rule, for programmatic use. */
  ruleId: string;
  ruleKind: "policy" | "exception";
}

/** A group of resources sharing one action and one source chain. */
export interface GrantGroup {
  action: string;
  /** Compressed resource paths, e.g. ["docs/*", "logs/2026"]. */
  paths: string[];
  source: SourceChain;
}

export interface ResourceTypeDiff {
  added: GrantGroup[];
  lost: GrantGroup[];
  unchanged: GrantGroup[];
}

export interface UserDiff {
  userId: string;
  /** Keyed by resource type. Types with no changes are omitted. */
  byResourceType: Record<string, ResourceTypeDiff>;
  /** True when nothing was added or lost in any resource type. */
  clean: boolean;
}

export interface DiffReport {
  users: UserDiff[];
}
