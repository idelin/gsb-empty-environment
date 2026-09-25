/**
 * 统一错误模型。
 * 每个错误都带 code 与结构化 details，details 中包含可定位的 JSON Pointer。
 */

export type ErrorCode =
  | 'INVALID_GRAPH'
  | 'NO_PATH'
  | 'AMBIGUOUS_PATH'
  | 'AMBIGUITY_PRESENT'
  | 'INVALID_ENVELOPE'
  | 'INPUT_SCHEMA_VIOLATION'
  | 'OUTPUT_SCHEMA_VIOLATION'
  | 'STEP_FAILED'
  | 'CHECKPOINT_NOT_FOUND'
  | 'CHECKPOINT_CORRUPT'
  | 'CHECKPOINT_INPUT_MISMATCH'
  | 'CHECKPOINT_TARGET_MISMATCH'
  | 'CHECKPOINT_GRAPH_MISMATCH'
  | 'CHECKPOINT_EXECUTION_MISMATCH';

export type Severity = 'error';

/** schema 校验问题：path 指向违规值（缺失字段指向父对象）。 */
export interface ValidationIssue {
  path: string;
  reason: string;
  expected?: string;
  actual?: string;
}

export interface MigrationErrorDetails {
  code: ErrorCode;
  /** 顶层错误的消息（英文稳定串，方便断言）。 */
  message: string;
  /** 数据相关错误：违规值的 JSON Pointer。 */
  path?: string;
  /** 迁移边 / 步骤标识，例如 "v1->v2(add_user_email)"。 */
  edgeId?: string;
  /** 图中的版本路径，例如 ["v1","v2","v4"]。 */
  versionPath?: string[];
  /** 图定义错误所在位置，例如 "/edges[1]" 或 "/edges[1]/from"。 */
  location?: string;
  /** 多个候选路径（歧义）。 */
  candidates?: string[][];
  /** 导致歧义的两个同优先路径对（其余候选同样可枚举）。 */
  tiedPaths?: [string[], string[]];
  /** 校验问题列表。 */
  issues?: ValidationIssue[];
  /** 步骤内部抛出的来源错误信息。 */
  cause?: string;
  /** 来源边（步骤）路径，如 "/edges[v1->v2(add_user_email)]"。 */
  causeLocation?: string;
  /** 期望 / 实际的哈希或已执行步骤数。 */
  expected?: string | number;
  actual?: string | number;
}

export class MigrationError extends Error {
  readonly details: MigrationErrorDetails;

  constructor(details: MigrationErrorDetails) {
    super(details.message);
    this.name = 'MigrationError';
    this.details = details;
  }

  toJSON(): MigrationErrorDetails {
    return { ...this.details };
  }
}

export function fail(details: MigrationErrorDetails): never {
  throw new MigrationError(details);
}
