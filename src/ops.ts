import type { VectorClock } from './version.ts';

export type Scalar = string | number | boolean | null;

/** 单个字段操作。opId 全局唯一，用于幂等去重；clock 为因果版本。 */
export interface Op {
  opId: string;
  txnId: string;
  replicaId: string;
  lamport: number;
  clock: VectorClock;
  field: string;
  /** null 表示删除该字段 */
  value: Scalar;
}

/** 一个事务提交后产生的原子批次：总线投递与落盘日志的最小单位。 */
export interface Batch {
  txnId: string;
  replicaId: string;
  ops: Op[];
}

/**
 * 显式合并规则：
 * - lww：最后写入胜，按 (lamport, replicaId) 全序决胜，结果确定。
 * - mv ：多值寄存器，并发写全部保留；读时按 resolver 决胜或暴露冲突。
 */
export type MergeRule =
  | { kind: 'lww' }
  | { kind: 'mv'; resolver: 'preserve' | 'max' | 'min' };

export type Schema = Record<string, MergeRule>;

export function ruleFor(schema: Schema, field: string): MergeRule {
  return schema[field] ?? { kind: 'lww' };
}
