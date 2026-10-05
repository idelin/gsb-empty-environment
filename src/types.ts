/**
 * 敏感级别（有序，数值越大越敏感）。
 */
export enum Sensitivity {
  Public = 0,
  Internal = 1,
  Confidential = 2,
  Sensitive = 3,
}

export const SENSITIVITY_LABELS: Record<Sensitivity, string> = {
  [Sensitivity.Public]: '公开',
  [Sensitivity.Internal]: '内部',
  [Sensitivity.Confidential]: '机密',
  [Sensitivity.Sensitive]: '敏感',
};

export function sensitivityLabel(level: Sensitivity): string {
  return SENSITIVITY_LABELS[level] ?? `未知(${level})`;
}

interface FieldBase {
  name: string;
  description?: string;
}

/** 原始字段：直接来自数据源，携带声明的敏感级别与覆盖人数（独立个体数）。 */
export interface SourceField extends FieldBase {
  kind: 'source';
  sensitivity: Sensitivity;
  population: number;
}

/** 派生字段：由一个或多个输入字段计算得到，敏感级别沿依赖传播（取输入最大值）。 */
export interface DerivedField extends FieldBase {
  kind: 'derive';
  inputs: string[];
  expression?: string;
}

/** 过滤字段：对输入做行级过滤，人数减少，敏感级别不变。 */
export interface FilteredField extends FieldBase {
  kind: 'filter';
  input: string;
  /** 过滤后保留的人数，必须小于输入人数。 */
  retainedPopulation: number;
  predicate?: string;
}

/**
 * 聚合字段：按组聚合（如均值/计数）。
 * 仅当最小组人数达到 minGroupSize（k 匿名阈值）时，敏感级别才允许降级到 downgradeTo；
 * 否则敏感级别继续向下游传播。
 */
export interface AggregateField extends FieldBase {
  kind: 'aggregate';
  input: string;
  /** 分组数（cohort 数量）。 */
  groupCount: number;
  /** 允许降级所需的最小分组人数 k。 */
  minGroupSize: number;
  /** 达标后降级到的目标级别。 */
  downgradeTo: Sensitivity;
  /** 实测最小组人数（可选）；缺省时按人数均匀分布下界 floor(输入人数 / 分组数) 估算。 */
  observedMinGroupSize?: number;
}

export type FieldDef = SourceField | DerivedField | FilteredField | AggregateField;

/** 导出计划：声明要导出的字段与允许的最高敏感级别。 */
export interface ExportPlan {
  name: string;
  exportFields: string[];
  maxAllowedSensitivity: Sensitivity;
}
