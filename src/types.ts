/**
 * 自动修复组合工具 —— 核心类型定义。
 *
 * 关键设计：单独适用的修复建议合在一起未必适用，因此兼容性由
 * 文本重叠、符号读写、显式互斥、前置闭包等多类证据共同判定，
 * 而不是仅看文本范围是否相交。
 */

/** 对某个文件的一次文本替换，区间采用原文偏移 [start, end)。 */
export interface TextEdit {
  file: string;
  start: number;
  end: number;
  replacement: string;
}

/** 应用期上下文：前置条件基于“当前”文件内容判定。 */
export interface ApplyContext {
  getFile(path: string): string | undefined;
}

/** 前置条件：选择前检查一次，应用过程中每次应用后重新检查。 */
export interface Precondition {
  /** 人类可读描述，会进入未选中/被丢弃的证据。 */
  description: string;
  /** 该条件读取的文件，用于在证据中定位“被谁破坏”。 */
  files?: string[];
  check(ctx: ApplyContext): boolean;
}

/** 一条自动修复建议。 */
export interface FixSuggestion {
  id: string;
  description: string;
  /** 收益（权重），求解目标是兼容子集的总收益最大。 */
  benefit: number;
  edits: TextEdit[];
  /** 前置建议 id：选中本建议必须同时选中它们。 */
  requires?: string[];
  preconditions?: Precondition[];
  /** 语义足迹：本建议会写（定义/修改）的符号。 */
  writesSymbols?: string[];
  /** 语义足迹：本建议依赖（读取）的符号。 */
  readsSymbols?: string[];
  /** 显式声明：本建议会使哪些建议失效。 */
  invalidates?: string[];
  /** 互斥组：同组最多选中一个。 */
  mutexGroup?: string;
}

export type ConflictKind =
  | 'text-overlap'
  | 'symbol-write-write'
  | 'symbol-write-read'
  | 'explicit-invalidation'
  | 'mutex-group';

/** 两条建议不兼容的证据。 */
export interface ConflictEvidence {
  kind: ConflictKind;
  otherId: string;
  detail: string;
}

/** 一条建议未被最终采纳的原因。 */
export type RejectionReason =
  | { kind: 'conflict'; conflicts: ConflictEvidence[] }
  | { kind: 'missing-prerequisite'; prerequisiteId: string; detail: string }
  | { kind: 'unknown-prerequisite'; prerequisiteId: string }
  | { kind: 'prerequisite-cycle'; cycle: string[] }
  | { kind: 'prerequisite-conflict'; detail: string }
  | { kind: 'initial-precondition-failed'; precondition: string }
  | { kind: 'post-apply-precondition-failed'; precondition: string; invalidatedBy: string[] }
  | { kind: 'dependent-dropped'; droppedPrerequisite: string }
  | { kind: 'benefit-too-low'; detail: string };

export interface Rejection {
  id: string;
  description: string;
  reasons: RejectionReason[];
}

export interface SelectedFix {
  id: string;
  description: string;
  benefit: number;
  /** 应用顺序（0 起）。 */
  order: number;
}

export interface CombineResult {
  /** 最终实际应用的修复，按应用顺序排列。 */
  selected: SelectedFix[];
  totalBenefit: number;
  /** 所有未采纳建议及其冲突/前置证据。 */
  rejected: Rejection[];
  /** 应用全部选中修复后的文件内容。 */
  finalFiles: Record<string, string>;
  /** 求解器是否精确求到最优（超出搜索预算时为 false，结果为当前最优下界）。 */
  exact: boolean;
}
