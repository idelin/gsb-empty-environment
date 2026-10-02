/**
 * 租约控制器核心类型定义。
 * 仅使用可擦除（erasable）TS 语法，以便 Node.js 原生类型剥离运行。
 */

/** 租约生命周期状态 */
export type LeaseState = 'active' | 'handover-pending' | 'released' | 'expired';

/** 租约上下文：持有者的完整租约凭证 */
export interface LeaseContext {
  /** 租约唯一标识（同一资源在持有者间传承时保持不变） */
  leaseId: string;
  /** 被保护的资源标识 */
  resourceId: string;
  /** 当前持有者节点 ID */
  holderId: string;
  /**  fencing token：单调递增，用于隔离脑裂后的旧主写入 */
  epoch: number;
  /** 交接代数：每发生一次成功 Handover 加一 */
  generation: number;
  /** 租约首次签发时间（ms） */
  acquiredAt: number;
  /** 租约到期时间（ms 时间戳） */
  expiresAt: number;
  /** 累计续期次数 */
  renewalCount: number;
}

/** 交接包：由协调中心签发、候选人凭此无缝继承租约 */
export interface HandoverPackage {
  leaseId: string;
  resourceId: string;
  fromNodeId: string;
  toNodeId: string;
  /** 新持有者的 fencing token（旧 epoch + 1） */
  epoch: number;
  generation: number;
  /** 签发时剩余租约时长（ms） */
  remainingTtlMs: number;
  /** 签发时间（ms） */
  issuedAt: number;
  /** 原租约到期时间，候选人原样继承，不重置计时 */
  expiresAt: number;
  /** 一次性交接凭证，防止伪造/重放 */
  token: string;
}

/** 交接完成事件 */
export interface HandoverCompletedEvent {
  leaseId: string;
  resourceId: string;
  fromNodeId: string;
  toNodeId: string;
  epoch: number;
  generation: number;
}

/** 租约相关错误，code 便于程序化判断 */
export class LeaseError extends Error {
  code: string;
  constructor(code: string, message: string) {
    super(message);
    this.name = 'LeaseError';
    this.code = code;
  }
}
