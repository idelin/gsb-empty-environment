/**
 * 数据模型：敏感级别、计算图节点、导出计划。
 *
 * 计算图是一个有向无环图（DAG），节点分为四类：
 *  - source    原始字段，携带基础敏感级别与总体人数
 *  - derive    派生字段，由一个或多个输入计算得到（如 concat、hash、round）
 *  - filter    过滤节点，按谓词缩减人数（行数减少会影响下游聚合的最小分组人数判断）
 *  - aggregate 聚合节点，按分组键统计；仅当所有分组人数 >= minGroupSize 时才允许降级
 */

export const Sensitivity = {
  Public: 0,
  Internal: 1,
  Confidential: 2,
  Secret: 3,
} as const;

export type SensitivityLevel = (typeof Sensitivity)[keyof typeof Sensitivity];

export const LEVEL_NAMES: Record<SensitivityLevel, string> = {
  0: "Public",
  1: "Internal",
  2: "Confidential",
  3: "Secret",
};

export type NodeId = string;

interface NodeBase {
  id: NodeId;
  /** 输出字段名 */
  field: string;
}

/** 原始字段节点 */
export interface SourceNode extends NodeBase {
  kind: "source";
  level: SensitivityLevel;
  /** 该数据集总体人数 */
  population: number;
}

/** 派生计算节点：隐藏原字段并不能消除它的泄露 */
export interface DeriveNode extends NodeBase {
  kind: "derive";
  inputs: NodeId[];
  /** 计算表达式描述，如 "concat(name, phone)" */
  expression: string;
}

/** 过滤节点：缩减人数，影响下游聚合是否满足最小分组人数 */
export interface FilterNode extends NodeBase {
  kind: "filter";
  inputs: [NodeId];
  /** 过滤谓词描述，如 "age >= 18" */
  predicate: string;
  /** 过滤后剩余人数（必须小于等于输入人数） */
  remainingPopulation: number;
}

/** 聚合节点：仅当每个分组人数都达到 minGroupSize 时才可降级到 downgradeTo */
export interface AggregateNode extends NodeBase {
  kind: "aggregate";
  inputs: [NodeId];
  groupBy: string[];
  /** 各分组实际人数（已反映上游过滤后的结果） */
  groupSizes: number[];
  /** 允许降级所需的最小分组人数 k */
  minGroupSize: number;
  /** 满足条件后可降级到的级别 */
  downgradeTo: SensitivityLevel;
}

export type PlanNode = SourceNode | DeriveNode | FilterNode | AggregateNode;

/** 导出计划：一张计算图 + 一组导出字段及其目标环境允许的最高级别 */
export interface ExportPlan {
  name: string;
  nodes: PlanNode[];
  exports: ExportRequest[];
}

export interface ExportRequest {
  /** 要导出的节点 id */
  nodeId: NodeId;
  /** 导出目标环境允许的最高敏感级别 */
  allowedLevel: SensitivityLevel;
}
