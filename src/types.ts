/** Shared types for the access-audit tool. */

export type CasePolicy = "sensitive" | "insensitive" | "auto";

export type DeclType = "file" | "dir";

export interface DeclEntry {
  /** Absolute or base-relative path. */
  path: string;
  type: DeclType;
  /** For dir entries: whether nested children are covered. Default true. */
  recursive?: boolean;
}

export interface Manifest {
  version?: number;
  /** Optional manifest-level case policy; CLI flag wins. */
  case?: CasePolicy;
  declarations: DeclEntry[];
}

export type AccessOp = "readFile" | "readdir" | "stat" | "exists";

export interface WorkloadOp {
  op: AccessOp;
  path: string;
}

export interface Workload {
  ops: WorkloadOp[];
}

/** One hop in a symlink expansion chain. */
export interface SymlinkHop {
  /** Absolute path of the symlink itself, as encountered. */
  at: string;
  /** Raw link text from readlink (may be relative). */
  target: string;
  /** Absolute path the link resolves to. */
  resolved: string;
}

export interface AccessRecord {
  op: AccessOp;
  /** Path as requested, normalized to an absolute path (symlinks NOT resolved). */
  path: string;
  /** Symlink expansion chain, in order of traversal. */
  chain: SymlinkHop[];
  /** Fully resolved absolute path, or null when resolution failed. */
  final: string | null;
  /** Whether the final target exists. */
  exists: boolean;
  /** True when the final target is a directory. */
  isDirectory: boolean;
  /** First path component that did not exist, when resolution failed. */
  missingAt?: string;
  /** Resolution/IO error code (e.g. ELOOP, EACCES). */
  error?: string;
}

export interface AccessLog {
  tool: string;
  base: string;
  startedAt: string;
  records: AccessRecord[];
}

export type AccessStatus =
  | "declared"
  | "undeclared"
  | "symlink-escape";

export interface AuditedAccess {
  op: AccessOp;
  path: string;
  final: string | null;
  exists: boolean;
  isDirectory: boolean;
  chain: SymlinkHop[];
  status: AccessStatus;
  /** Number of times this exact access was observed (duplicates collapsed). */
  count: number;
  /** Declaration that authorized this access, when declared. */
  authorizedBy?: DeclEntry;
  /** Human-readable reason for undeclared / escape outcomes. */
  reason?: string;
  error?: string;
}

export interface SupplementEntry {
  path: string;
  type: DeclType;
  recursive?: boolean;
  reason: string;
}

export interface AuditReport {
  casePolicy: Exclude<CasePolicy, "auto">;
  summary: {
    totalAccesses: number;
    uniqueAccesses: number;
    declared: number;
    undeclared: number;
    symlinkEscapes: number;
    missing: number;
  };
  accesses: AuditedAccess[];
  supplement: SupplementEntry[];
}
