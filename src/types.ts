export type RuleMode = 'override' | 'append';

export interface ApprovalRule {
  pattern: string;
  owners: string[];
  mode: RuleMode;
  minApprovals?: number;
}

export interface ChangedFile {
  path: string;
  oldPath?: string;
}

export interface CheckInput {
  author: string;
  rules: ApprovalRule[];
  files: ChangedFile[];
  approvals: string[];
  candidates: string[];
  defaultMinApprovals?: number;
}

export interface RequirementReport {
  path: string;
  owners: string[];
  required: number;
  approvedBy: string[];
  missing: number;
}

export interface FileReport {
  path: string;
  oldPath?: string;
  requirements: RequirementReport[];
  satisfied: boolean;
}

export interface BlockingFile {
  path: string;
  oldPath?: string;
  requirementPath: string;
  owners: string[];
  requiredApprovals: number;
  existingApprovals: number;
  eligibleCandidates: number;
  shortfall: number;
}

export interface CheckResult {
  ok: boolean;
  addedApprovers: string[];
  blockingFiles: BlockingFile[];
  files: FileReport[];
}
