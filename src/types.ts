// 输入记录与输出变更的类型定义。
// 日志按到达顺序消费；告警按事件时间语义计算。

export type Kind = 'A' | 'B' | 'C';

/** 事件的新修订：可改变类型或时间。 */
export interface UpsertRecord {
  type: 'upsert';
  id: string;      // 稳定事件 ID
  rev: number;     // 修订号，单调递增
  kind: Kind;
  time: number;    // 事件时间
}

/** 撤销（tombstone）修订：撤销该 ID 的事件。time 用于迟到判定。 */
export interface RetractRecord {
  type: 'retract';
  id: string;
  rev: number;
  time: number;
}

/** 水位线：单调递增，time 之前（含允许迟到范围外）的事件被视为完整。 */
export interface WatermarkRecord {
  type: 'watermark';
  time: number;
}

export type InputRecord = UpsertRecord | RetractRecord | WatermarkRecord;

/** 输出变更流：upsert 建立/恢复告警，retract 撤销告警。 */
export interface UpsertChange {
  type: 'upsert';
  alertId: string;
  aId: string;
  bId: string;
}

export interface RetractChange {
  type: 'retract';
  alertId: string;
}

export type Change = UpsertChange | RetractChange;

/** 每个 (AID,BID) 对有稳定告警 ID。 */
export function alertIdOf(aId: string, bId: string): string {
  return `A:${aId}|B:${bId}`;
}
