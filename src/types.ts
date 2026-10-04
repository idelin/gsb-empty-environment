/**
 * 核心数据模型：修复建议、文本编辑、前置条件、冲突证据。
 */

export interface Position {
  /** 0 起始行号 */
  line: number;
  /** 0 起始字符列 */
  character: number;
}

export interface TextRange {
  start: Position;
  end: Position;
}

/** 一次文本替换：把 [start, end) 替换为 newText。start == end 表示插入。 */
export interface TextEdit {
  file: string;
  range: TextRange;
  newText: string;
}

/** 前置条件：针对某个文件当前内容的断言，应用修复前必须成立。 */
export type Precondition =
  | { kind: "fileExists"; file: string }
  | { kind: "contains"; file: string; pattern: string; isRegex?: boolean }
  | { kind: "notContains"; file: string; pattern: string; isRegex?: boolean }
  | {
      kind: "matches";
      file: string;
      regex: string;
      minCount?: number;
      maxCount?: number;
    };

export interface FixSuggestion {
  id: string;
  description: string;
  /** 收益，越大越优先；必须为非负有限数 */
  benefit: number;
  edits: TextEdit[];
  preconditions: Precondition[];
  /** 前置修复：选中本建议必须同时选中这些建议（传递闭包） */
  requires?: string[];
  /**
   * 语义冲突标签：共享同一标签的建议互斥。
   * 用于表达“文本范围不重叠但语义上不能共存”的冲突。
   */
  conflictKeys?: string[];
}

export type FileState = Record<string, string>;

export interface CombineInput {
  files: FileState;
  suggestions: FixSuggestion[];
  /** 最多选择的修复数，默认 25 */
  maxSelected?: number;
}

export type ConflictKind =
  | "edit-overlap"
  | "conflict-key"
  | "precondition-invalidated";

/** 两个建议之间不兼容的证据 */
export interface ConflictEvidence {
  kind: ConflictKind;
  otherId: string;
  detail: string;
}

export type RejectReason =
  | { kind: "invalid"; detail: string }
  | { kind: "conflict"; evidence: ConflictEvidence[] }
  | { kind: "requires-excluded"; requiredId: string; chain: string[]; detail: string }
  | {
      kind: "precondition-failed-after-apply";
      detail: string;
      invalidatedBy: string[];
    }
  | { kind: "capacity"; detail: string };

export interface RejectedSuggestion {
  id: string;
  description: string;
  benefit: number;
  reasons: RejectReason[];
}

export interface AppliedFix {
  id: string;
  description: string;
  benefit: number;
}

export interface CombineResult {
  selected: AppliedFix[];
  totalBenefit: number;
  /** 未选择建议及其冲突/排除证据 */
  rejected: RejectedSuggestion[];
  /** 应用所选修复后的文件内容 */
  files: FileState;
  /** 求解是否达到最优（搜索超预算时可能为近似最优） */
  optimal: boolean;
}
