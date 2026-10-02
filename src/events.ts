/**
 * 审计事件模型：租约全生命周期 + 仲裁器锁状态变迁的结构化流水。
 */
import type { LogicalTimestamp } from './logical-clock.ts';

export type EventKind =
  /** 租约申请到达仲裁器（记录到达次序） */
  | 'LEASE_REQUESTED'
  /** 租约被授予 */
  | 'LEASE_GRANTED'
  /** 租约续期成功 */
  | 'LEASE_RENEWED'
  /** 租约持有者移交 */
  | 'LEASE_TRANSFERRED'
  /** 租约超时失效 */
  | 'LEASE_EXPIRED'
  /** 申请被拒绝（含拒绝原因） */
  | 'LEASE_REJECTED'
  /** 检测到竞态冲突（多请求争抢同一租约） */
  | 'CONTENTION_DETECTED'
  /** 仲裁器内部互斥锁被获取 */
  | 'ARBITER_LOCK_ACQUIRED'
  /** 仲裁器内部互斥锁被释放 */
  | 'ARBITER_LOCK_RELEASED';

export type RejectReason =
  /** 租约已被其他节点持有 */
  | 'LEASE_HELD'
  /** 操作者不是当前持有者 */
  | 'NOT_HOLDER'
  /** 租约已过期 */
  | 'LEASE_EXPIRED'
  /** fencing token 过期（脑裂防护） */
  | 'FENCING_TOKEN_STALE';

export interface AuditEvent {
  /** 追踪器内单调递增序号（记录即到达的物理次序） */
  seq: number;
  kind: EventKind;
  leaseId: string;
  /** 触发该事件的主体（节点或 'arbiter'） */
  actorId: string;
  /** 记录者（仲裁器）的逻辑时间戳 */
  clock: LogicalTimestamp;
  /** 物理墙钟（毫秒），仅作参考，不作排序依据 */
  wallTimeMs: number;
  /** 因果链：本事件由哪些事件（seq）直接导致 */
  causedBy: number[];
  /** 事件负载 */
  details: Record<string, unknown>;
}

export interface LeaseRequestedDetails {
  /** 请求方自己盖章的逻辑时间戳（竞态时双方可能相同） */
  requesterClock: LogicalTimestamp;
  ttlMs: number;
}

export interface LeaseGrantedDetails {
  holderId: string;
  fencingToken: number;
  expiresAtMs: number;
}

export interface LeaseRejectedDetails {
  reason: RejectReason;
  /** 当前持有者（若拒绝原因是 LEASE_HELD） */
  currentHolderId?: string;
  currentFencingToken?: number;
}

export interface ContentionDetectedDetails {
  /** 胜出的申请事件 seq */
  winnerRequestSeq: number;
  winnerNodeId: string;
  /** 落败的申请事件 seq */
  loserRequestSeq: number;
  loserNodeId: string;
  /** 冲突发生时仲裁器锁状态 */
  arbiterLockState: 'IDLE' | 'LOCKED';
  /** 双方请求时间戳是否落在同一毫秒 */
  sameMillisecond: boolean;
}

export interface ArbiterLockDetails {
  /** 触发本次锁变迁的操作 */
  operation: string;
  /** 变迁前状态 */
  from: 'IDLE' | 'LOCKED';
  /** 变迁后状态 */
  to: 'IDLE' | 'LOCKED';
}
