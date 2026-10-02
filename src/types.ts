/**
 * 多分区确定性事务状态机 —— 公共类型定义。
 *
 * 确定性来源：所有状态变更只由「带全局序号 seq 的本地日志事件」驱动。
 * 无论事件以何种网络顺序到达，状态机都按 seq 顺序回放，因此最终状态确定。
 */

export type PartitionId = number;
export type TxnId = string;

export type EventKind = 'prepare' | 'commit' | 'abort';

/** 确定性日志事件（两阶段：prepare -> commit/abort）。 */
export interface LogEvent {
  /** 全局单调序号，由日志定序器分配，是回放的唯一顺序依据。 */
  seq: number;
  txnId: TxnId;
  kind: EventKind;
  /** 涉及的分区。 */
  partitions: PartitionId[];
  /** 读集：分区 -> 键列表。 */
  reads: Record<PartitionId, string[]>;
  /** 写集：分区 -> 键 -> 值。 */
  writes: Record<PartitionId, Record<string, string>>;
}

/** 两事务在某一键上的冲突明细。 */
export interface ConflictDetail {
  other: TxnId;
  partition: PartitionId;
  key: string;
  kind: 'write-write' | 'write-read' | 'read-write';
}

/** 未决冲突的决胜证据（可审计）。 */
export interface TieBreakEvidence {
  kind: 'conflict-tiebreak';
  /** 触发决胜的 commit 事件位点。 */
  decidingSeq: number;
  winner: TxnId;
  losers: TxnId[];
  /** 决胜规则（确定性，可复述）。 */
  rule: string;
  /** 冲突事务依赖图的拓扑排序结果（决胜顺序）。 */
  topoOrder: TxnId[];
  conflicts: ConflictDetail[];
}

/** 内存有界导致的未决事务驱逐证据（可审计）。 */
export interface EvictionEvidence {
  kind: 'memory-bound-eviction';
  evicted: TxnId;
  reason: string;
  pendingSize: number;
  maxPending: number;
}

export type Evidence = TieBreakEvidence | EvictionEvidence;

/** 快照：切片点之后的日志重放即可恢复完全一致的状态。 */
export interface Snapshot {
  /** 切片点：序号 < nextSeq 的事件已应用，可从日志中截断。 */
  nextSeq: number;
  /** 各分区键值状态（键已排序，序列化确定）。 */
  partitions: Record<PartitionId, Record<string, string>>;
  /** 切片点处仍未决（已 prepare 未 commit/abort）的事务事件。 */
  pending: LogEvent[];
}
