export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonObject = { [key: string]: JsonValue };

/** 互斥条件：when 中所有等值同时成立时，该组合不可行。 */
export interface ForbidConstraint {
  forbid: Record<string, JsonValue>;
}

/** 有限域依赖：when 全部匹配时，allow 中每个轴的取值必须落在给定值列表内。 */
export interface RequireConstraint {
  require: Record<string, JsonValue>;
  allow: Record<string, JsonValue[]>;
}

export type Constraint = ForbidConstraint | RequireConstraint;

export interface ParamSpec {
  /** 轴定义：轴名 -> 有序值域（数组有序，决定迭代顺序）。 */
  axes: Record<string, JsonValue[]>;
  /** 轴间约束：有限域依赖与互斥条件。 */
  constraints?: Constraint[];
}

export interface ExpandOptions {
  /** 声明身份：进入记录的点分路径列表，用于去重。 */
  identity?: string[];
  /** 本次调用最多产出的记录数（额度）。 */
  budget?: number;
  /** 上一次额度耗尽时返回的可恢复游标。 */
  cursor?: string;
}

export interface CollectResult {
  records: JsonValue[];
  /** 是否已穷举完毕。 */
  done: boolean;
  /** 未穷举完时返回的可恢复游标；done 时不存在。 */
  cursor?: string;
  /** 含本次在内已累计产出的记录数。 */
  emitted: number;
  /** 完成时为精确总量；预算中断时为 null（不伪报总量）。 */
  total: number | null;
}
