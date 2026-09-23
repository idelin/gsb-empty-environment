// 核心类型：操作日志、状态操作、补丁与检查点。
// 仅使用可被 Node --experimental-strip-types 擦除的语法（无 enum/namespace/参数属性）。

export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type StateValue = JsonValue;

// ---- 状态操作（可回放、可逆） ----

export type StateOp =
  | { kind: 'kv.set'; key: string; value: StateValue }
  | { kind: 'kv.add'; key: string; delta: number };

export type Patch = { key: string; before: StateValue | undefined; after: StateValue | undefined };

// ---- 日志记录体 ----

export type TxId = string;

export type RecordBody =
  | { kind: 'op'; opId: string; op: StateOp; inverse: Patch[]; tx: TxId | null; effectId: string | null }
  | { kind: 'tx.begin'; tx: TxId }
  | { kind: 'tx.commit'; tx: TxId }
  | { kind: 'tx.abort'; tx: TxId }
  | { kind: 'effect'; effectId: string; detail: string }
  | { kind: 'branch'; branch: string; baseSeq: number; baseHash: string };

export type RecordKind = RecordBody['kind'];

export interface LogRecord {
  seq: number;
  prev: string; // 前一条记录的哈希（前缀链指针），首条为 GENESIS
  body: RecordBody;
  hash: string; // 本条记录哈希 = sha256(canonical({seq, prev, body}))
}

// ---- 检查点：绑定到某个日志前缀摘要 ----

export interface Checkpoint {
  id: string;
  uptoSeq: number; // 覆盖的日志前缀长度（记录条数）
  prefixHash: string; // 该前缀的摘要（= 第 uptoSeq 条记录的哈希，或 GENESIS）
  state: Record<string, StateValue>; // 该前缀对应的已提交状态
  appliedOpIds: string[]; // 已应用的操作 ID（去重依据）
  confirmedEffects: string[]; // 已确认的外部副作用（不重复触发依据）
  pendingEffects: string[]; // 已触发但未确认的副作用（恢复时必须重新驱动）
  hash: string; // 检查点自身摘要，防篡改
}

export interface CompactedSegment {
  checkpoint: Checkpoint;
  anchor: LogRecord; // 哨兵记录：body 携带检查点摘要，seq=0，作为压缩段的链头
}

export interface Verification {
  ok: boolean;
  validPrefix: number; // 通过校验的前缀长度（记录条数）
  tipHash: string; // 有效前缀的摘要
  error: { seq: number; reason: string } | null; // 首个损坏点的操作序号与原因
}

export class LogCorruptionError extends Error {
  seq: number;
  constructor(seq: number, reason: string) {
    super(`日志损坏 @ 操作序号 seq=${seq}: ${reason}`);
    this.name = 'LogCorruptionError';
    this.seq = seq;
  }
}

export class SpliceError extends Error {
  constructor(reason: string) {
    super(`拼接被拒绝: ${reason}`);
    this.name = 'SpliceError';
  }
}

export class CheckpointError extends Error {
  constructor(reason: string) {
    super(`检查点无效: ${reason}`);
    this.name = 'CheckpointError';
  }
}
