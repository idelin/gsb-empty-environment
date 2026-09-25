/**
 * 义务类型与义务专用合并代数。
 *
 * 每种义务有自己的合并代数：
 *  - limit:          限额，取更严格值（上限取 min，下限取 max）
 *  - mask:           遮蔽字段，取并集（顺序无关）
 *  - audit:          审计标签，取并集（顺序无关，满足交换律）
 *  - format:         输出格式，互斥选择；两个不同格式 => 明确冲突失败，绝不静默择一
 *  - dependentLimit: 具有依赖关系的限额约束（key 的取值须 >= relativeTo 的取值 + minGap）
 *  - orderedSteps:   顺序敏感义务，合并时显式保序（不满足交换律）
 *
 * 每条义务携带 source（来源规则 id），用于生成来源证据。
 */

export type Obligation =
  | LimitObligation
  | MaskObligation
  | AuditObligation
  | FormatObligation
  | DependentLimitObligation
  | OrderedStepsObligation;

export interface LimitObligation {
  kind: 'limit';
  id: string;
  /** 限额键，如 "maxRows"、"maxAmount" */
  key: string;
  /** max = 上限（更严格 = 更小）；min = 下限（更严格 = 更大） */
  direction: 'max' | 'min';
  value: number;
  source: string;
}

export interface MaskObligation {
  kind: 'mask';
  id: string;
  fields: readonly string[];
  source: string;
}

export interface AuditObligation {
  kind: 'audit';
  id: string;
  tags: readonly string[];
  source: string;
}

export interface FormatObligation {
  kind: 'format';
  id: string;
  format: string;
  source: string;
}

export interface DependentLimitObligation {
  kind: 'dependentLimit';
  id: string;
  /** 受约束的限额键 */
  key: string;
  /** 依赖的限额键 */
  relativeTo: string;
  /** 要求 value(key) >= value(relativeTo) + minGap */
  minGap: number;
  source: string;
}

export interface OrderedStepsObligation {
  kind: 'orderedSteps';
  id: string;
  /** 顺序敏感的执行步骤，合并时按规则顺序显式保序 */
  steps: readonly string[];
  source: string;
}

let counter = 0;
const nextId = (prefix: string): string => `${prefix}#${++counter}`;

export const limit = (
  key: string, direction: 'max' | 'min', value: number, source: string,
): LimitObligation => ({ kind: 'limit', id: nextId('limit'), key, direction, value, source });

export const mask = (fields: readonly string[], source: string): MaskObligation =>
  ({ kind: 'mask', id: nextId('mask'), fields: Object.freeze([...fields]), source });

export const audit = (tags: readonly string[], source: string): AuditObligation =>
  ({ kind: 'audit', id: nextId('audit'), tags: Object.freeze([...tags]), source });

export const format = (fmt: string, source: string): FormatObligation =>
  ({ kind: 'format', id: nextId('format'), format: fmt, source });

export const dependentLimit = (
  key: string, relativeTo: string, minGap: number, source: string,
): DependentLimitObligation =>
  ({ kind: 'dependentLimit', id: nextId('dep'), key, relativeTo, minGap, source });

export const orderedSteps = (steps: readonly string[], source: string): OrderedStepsObligation =>
  ({ kind: 'orderedSteps', id: nextId('steps'), steps: Object.freeze([...steps]), source });

/** 顺序无关义务的规范化键：排序后比较，用于交换律验证。 */
export const normalizeUnordered = (items: readonly string[]): string[] =>
  [...new Set(items)].sort();
