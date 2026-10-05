export type RuleMode = "override" | "append";

export interface Rule {
  /** Glob pattern matched against the repo-relative path. */
  pattern: string;
  /** Owner usernames for this rule. */
  owners: string[];
  /** Approvals required from the effective owner set. Defaults to 1. */
  threshold?: number;
  /** "override" replaces the accumulated owner set, "append" unions into it. */
  mode?: RuleMode;
}

export interface FileEntry {
  /** New (current) path of the changed file. */
  path: string;
  /** Original path when the file was renamed. */
  oldPath?: string;
}

export interface CheckerInput {
  /** Change author; their own approval never counts. */
  author: string;
  /** Approvals already given. */
  approvals?: string[];
  /** Users that may be asked for additional approval (max 20). */
  candidates?: string[];
  files: FileEntry[];
  /** Rules in ascending priority: later rules win. */
  rules: Rule[];
}

export interface PathRequirement {
  path: string;
  owners: string[];
  threshold: number;
}

export interface FileReport {
  path: string;
  oldPath?: string;
  requirements: Array<PathRequirement & { approvals: number; satisfied: boolean }>;
  satisfied: boolean;
}

export interface BlockingFile {
  path: string;
  failedPath: string;
  owners: string[];
  threshold: number;
  maxApprovals: number;
}

export interface CheckerOutput {
  ok: boolean;
  /** Minimum additional approvers to request (empty when ok is false). */
  added: string[];
  /** All effective approvers: existing approvals plus `added`. */
  approvers: string[];
  files: FileReport[];
  /** Populated when no candidate set can satisfy every file. */
  blocking: BlockingFile[];
}
