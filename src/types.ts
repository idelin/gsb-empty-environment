/** 租约凭证：持有者凭此向协调中心证明自己的主角色身份。 */
export interface LeaseToken {
  resourceId: string;
  holderId: string;
  /** 单调递增的栅栏令牌，用于防止脑裂后的旧主继续写入。 */
  fencingToken: number;
  /** 过期时刻（epoch ms，由协调中心时钟给出）。 */
  expiresAt: number;
  /** 租约代数，每次易主 +1。 */
  leaseVersion: number;
}

/** 随租约一起迁移的业务上下文（如待回放写队列、会话状态等）。 */
export type LeaseContext = Record<string, unknown>;

/** 交接包：由当前持有者发起、候选人凭它无缝继承剩余租约上下文。 */
export interface HandoverPackage {
  handoverId: string;
  resourceId: string;
  fromHolderId: string;
  toCandidateId: string;
  /** 预留给候选人的下一个栅栏令牌。 */
  fencingToken: number;
  /** 发起交接时剩余的租约时长，候选人接管后按此时长续命，避免等待自然超时。 */
  remainingTtlMs: number;
  leaseVersion: number;
  /** 冻结期间累积的待移交上下文。 */
  context: LeaseContext;
  preparedAt: number;
}

export class LeaseError extends Error {
  readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.name = 'LeaseError';
    this.code = code;
  }
}
