export type CasePolicy = "sensitive" | "insensitive" | "auto";
export type EffectiveCasePolicy = "sensitive" | "insensitive";

export type DeclKind = "file" | "directory" | "executable";

export type AccessKind = "read" | "write" | "exec";

export interface DeclaredManifest {
  casePolicy?: CasePolicy;
  files?: string[];
  directories?: string[];
  executables?: string[];
}

export interface AccessEvent {
  path: string;
  kind?: AccessKind;
}

export interface SymlinkHop {
  /** Absolute path of the symlink itself. */
  at: string;
  /** Raw target text returned by readlink. */
  target: string;
  /** Absolute path the hop resolves to (target joined against the link's dir). */
  resolvedTo: string;
}

export interface ResolvedPath {
  input: string;
  /** Lexical absolute path of the input (no symlink resolution). */
  absolute: string;
  /** Canonical path with every existing symlink component resolved. */
  canonical: string;
  /** Ordered symlink expansion chain. */
  hops: SymlinkHop[];
  exists: boolean;
  /** First path component that does not exist, when exists === false. */
  missingAt?: string;
  /** True when a symlink loop (or hop-limit overflow) was detected. */
  loop: boolean;
}

export interface PreparedDecl {
  raw: string;
  kind: DeclKind;
  absolute: string;
  canonical: string;
  exists: boolean;
}

export type AccessStatus = "declared" | "undeclared" | "unverifiable";

export interface AccessRecord {
  path: string;
  kinds: AccessKind[];
  /** Number of raw trace events folded into this record. */
  count: number;
  resolved: ResolvedPath;
  status: AccessStatus;
  /** Declaration that authorized this access, when status === "declared". */
  matchedBy?: { kind: DeclKind; path: string };
  /** Why the access is not authorized, when status !== "declared". */
  reason?: string;
}

export interface SupplementManifest {
  files: string[];
  directories: string[];
  executables: string[];
}

export interface AuditReport {
  casePolicy: EffectiveCasePolicy;
  summary: {
    totalEvents: number;
    uniqueAccesses: number;
    duplicateEvents: number;
    declared: number;
    undeclared: number;
    unverifiable: number;
    missingPaths: number;
  };
  accesses: AccessRecord[];
  undeclared: AccessRecord[];
  supplement: SupplementManifest;
}
