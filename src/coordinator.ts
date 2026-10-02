import { randomUUID } from 'node:crypto';
import { LeaseError } from './types.ts';
import type { HandoverPackage, LeaseContext, LeaseToken } from './types.ts';

export interface CoordinatorOptions {
  /** 允许的最小/最大租约时长，动态续期请求会被钳制到该区间。 */
  minTtlMs?: number;
  maxTtlMs?: number;
  /** 默认租约时长。 */
  defaultTtlMs?: number;
  /** 可注入时钟，便于测试。 */
  now?: () => number;
}

interface LeaseRecord {
  holderId: string;
  fencingToken: number;
  expiresAt: number;
  leaseVersion: number;
  context: LeaseContext;
  status: 'active' | 'transferring';
  pendingHandover?: HandoverPackage;
}

/**
 * 租约协调中心：负责租约的授予、动态续期、主动移交（Handover）与释放。
 * 所有状态变更在单方法调用内原子完成，保证移交过程无中间空窗。
 */
export class LeaseCoordinator {
  private readonly records = new Map<string, LeaseRecord>();
  private readonly minTtlMs: number;
  private readonly maxTtlMs: number;
  private readonly defaultTtlMs: number;
  private readonly now: () => number;

  constructor(options: CoordinatorOptions = {}) {
    this.minTtlMs = options.minTtlMs ?? 500;
    this.maxTtlMs = options.maxTtlMs ?? 30_000;
    this.defaultTtlMs = options.defaultTtlMs ?? 5_000;
    this.now = options.now ?? Date.now;
  }

  /** 申请租约。资源空闲或已过期时授予；否则抛出 LEASE_HELD。 */
  acquire(resourceId: string, holderId: string, ttlMs?: number, context: LeaseContext = {}): LeaseToken {
    const record = this.records.get(resourceId);
    if (record && record.expiresAt > this.now() && record.holderId !== holderId) {
      throw new LeaseError('LEASE_HELD', `resource ${resourceId} is held by ${record.holderId}`);
    }
    const fencingToken = (record?.fencingToken ?? 0) + 1;
    const leaseVersion = record && record.holderId === holderId ? record.leaseVersion : (record?.leaseVersion ?? 0) + 1;
    const next: LeaseRecord = {
      holderId,
      fencingToken,
      expiresAt: this.now() + this.clampTtl(ttlMs),
      leaseVersion,
      context: { ...context },
      status: 'active',
    };
    this.records.set(resourceId, next);
    return this.toToken(resourceId, next);
  }

  /**
   * 动态续期：持有者可按需请求新的 TTL，协调中心将其钳制到
   * [minTtlMs, maxTtlMs] 区间后生效，支持随负载/健康状况动态调整。
   */
  renew(token: LeaseToken, requestedTtlMs?: number): LeaseToken {
    const record = this.requireHolder(token);
    record.expiresAt = this.now() + this.clampTtl(requestedTtlMs);
    return this.toToken(token.resourceId, record);
  }

  /**
   * 第一步（持有者侧）：发起主动移交。校验当前持有者身份后，
   * 将租约置为 transferring 并生成交接包；剩余租约上下文随包冻结，
   * 候选人无需等待自然超时即可接管。
   */
  prepareHandover(token: LeaseToken, candidateId: string, contextPatch: LeaseContext = {}): HandoverPackage {
    const record = this.requireHolder(token);
    if (record.status === 'transferring') {
      throw new LeaseError('HANDOVER_IN_PROGRESS', `handover already in progress for ${token.resourceId}`);
    }
    if (candidateId === token.holderId) {
      throw new LeaseError('INVALID_CANDIDATE', 'candidate must differ from current holder');
    }
    const remainingTtlMs = record.expiresAt - this.now();
    if (remainingTtlMs <= 0) {
      throw new LeaseError('LEASE_EXPIRED', `lease for ${token.resourceId} already expired`);
    }
    const pkg: HandoverPackage = {
      handoverId: randomUUID(),
      resourceId: token.resourceId,
      fromHolderId: token.holderId,
      toCandidateId: candidateId,
      fencingToken: record.fencingToken + 1,
      remainingTtlMs,
      leaseVersion: record.leaseVersion,
      context: { ...record.context, ...contextPatch },
      preparedAt: this.now(),
    };
    record.status = 'transferring';
    record.pendingHandover = pkg;
    return pkg;
  }

  /**
   * 第二步（候选人侧）：凭交接包原子接管租约。
   * 接管瞬间即获得新的过期时刻（now + 剩余 TTL），全程无租约空窗；
   * 栅栏令牌递增，旧持有者此后的写入/续期一律被拒绝。
   */
  acceptHandover(pkg: HandoverPackage): LeaseToken {
    const record = this.records.get(pkg.resourceId);
    if (!record || record.status !== 'transferring' || record.pendingHandover?.handoverId !== pkg.handoverId) {
      throw new LeaseError('HANDOVER_NOT_PENDING', `no pending handover ${pkg.handoverId}`);
    }
    if (record.holderId !== pkg.fromHolderId || pkg.toCandidateId == null) {
      throw new LeaseError('HANDOVER_MISMATCH', 'handover package does not match current lease');
    }
    if (record.expiresAt <= this.now()) {
      record.status = 'active';
      record.pendingHandover = undefined;
      throw new LeaseError('LEASE_EXPIRED', 'lease expired before handover completed');
    }
    record.holderId = pkg.toCandidateId;
    record.fencingToken = pkg.fencingToken;
    record.leaseVersion += 1;
    record.expiresAt = this.now() + pkg.remainingTtlMs;
    record.context = { ...pkg.context };
    record.status = 'active';
    record.pendingHandover = undefined;
    return this.toToken(pkg.resourceId, record);
  }

  /** 主动释放（无候选人时退化为普通释放）。 */
  release(token: LeaseToken): void {
    const record = this.requireHolder(token);
    this.records.delete(token.resourceId);
    void record;
  }

  /** 校验一次携带栅栏令牌的写入是否合法（持有且未过期）。 */
  validateWrite(token: LeaseToken): void {
    this.requireHolder(token);
  }

  /** 查询当前持有者（仅观测用）。 */
  currentHolder(resourceId: string): string | undefined {
    const record = this.records.get(resourceId);
    return record && record.expiresAt > this.now() ? record.holderId : undefined;
  }

  private requireHolder(token: LeaseToken): LeaseRecord {
    const record = this.records.get(token.resourceId);
    if (!record) {
      throw new LeaseError('LEASE_NOT_FOUND', `no lease for ${token.resourceId}`);
    }
    if (record.holderId !== token.holderId || record.fencingToken !== token.fencingToken) {
      throw new LeaseError(
        'FENCING_VIOLATION',
        `stale token from ${token.holderId} (fencing ${token.fencingToken}, current ${record.fencingToken})`,
      );
    }
    if (record.expiresAt <= this.now()) {
      throw new LeaseError('LEASE_EXPIRED', `lease for ${token.resourceId} expired`);
    }
    return record;
  }

  private clampTtl(ttlMs?: number): number {
    const ttl = ttlMs ?? this.defaultTtlMs;
    return Math.min(this.maxTtlMs, Math.max(this.minTtlMs, ttl));
  }

  private toToken(resourceId: string, record: LeaseRecord): LeaseToken {
    return {
      resourceId,
      holderId: record.holderId,
      fencingToken: record.fencingToken,
      expiresAt: record.expiresAt,
      leaseVersion: record.leaseVersion,
    };
  }
}
