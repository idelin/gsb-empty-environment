import type { ValidationError, ValidationResult } from './validator.ts';

/**
 * 五类对比结果：
 * - new-failure:    旧 schema 通过、新 schema 失败（旧通过新失败，单列）
 * - fixed:          旧 schema 失败、新 schema 通过（已修复）
 * - reason-changed: 两边都失败，但命中的规则路径集合不同（原因变化）
 * - still-failing:  两边都失败，且规则路径集合相同
 * - still-passing:  两边都通过
 */
export type ChangeCategory =
  | 'new-failure'
  | 'fixed'
  | 'reason-changed'
  | 'still-failing'
  | 'still-passing';

export const CATEGORY_LABELS: Record<ChangeCategory, string> = {
  'new-failure': '新增失败（旧通过、新失败）',
  fixed: '已修复',
  'reason-changed': '原因变化（两边都失败、规则不同）',
  'still-failing': '仍然失败（规则相同）',
  'still-passing': '通过',
};

export const CATEGORY_ORDER: ChangeCategory[] = [
  'new-failure',
  'fixed',
  'reason-changed',
  'still-failing',
  'still-passing',
];

export interface SampleComparison {
  sampleId: string;
  category: ChangeCategory;
  oldErrors: ValidationError[];
  newErrors: ValidationError[];
}

/** 规则路径集合：以失败关键字的 schemaPath 标识“触发了哪条规则”。 */
export function rulePaths(errors: ValidationError[]): string[] {
  return [...new Set(errors.map((error) => error.schemaPath))].sort();
}

export function classify(oldResult: ValidationResult, newResult: ValidationResult): ChangeCategory {
  if (oldResult.valid && newResult.valid) {
    return 'still-passing';
  }
  if (oldResult.valid && !newResult.valid) {
    return 'new-failure';
  }
  if (!oldResult.valid && newResult.valid) {
    return 'fixed';
  }
  const oldRules = rulePaths(oldResult.errors).join('\n');
  const newRules = rulePaths(newResult.errors).join('\n');
  return oldRules === newRules ? 'still-failing' : 'reason-changed';
}

export function compareSample(
  sampleId: string,
  oldResult: ValidationResult,
  newResult: ValidationResult,
): SampleComparison {
  return {
    sampleId,
    category: classify(oldResult, newResult),
    oldErrors: oldResult.errors,
    newErrors: newResult.errors,
  };
}
