import { randomUUID } from 'node:crypto';
import { EventEmitter } from 'node:events';
import type {
  HandoverCompletedEvent,
  HandoverPackage,
  LeaseContext,
  LeaseState,
} from './types.ts';
import { LeaseError } from './types.ts';

interface LeaseRecord {
  context: LeaseContext;
  state: LeaseState;
  pendingHandover: HandoverPackage | null;
}

export interface CoordinatorOptions {
  /** 可注入时钟，便于测试；默认 Date.now */
  now?: () => number;
}

/**
 * 租约协调中心。
 * 负责租约签发、动态续期、主动移交（Handover）协议与 fencing 校验。
 */
export class LeaseCoordinator extends EventEmitter {
  private leases = new Map<string, LeaseRecord>();
  private leaseIdByResource = new Map<string, string>();
  private now: () => number;

  constructor(options: CoordinatorOptions = {}) {
    super();
    this.now = options.now ?? Date.now;
  }

  /** 申请租约；资源已被活跃租约占有时抛错 */
  acquire(resourceId: string, holderId: string, ttlMs: number): LeaseContext {
    if (ttlMs <= 0) throw new LeaseError('invalid-argument', 'ttlMs must be positive');
    const existingId = this.leaseIdByResource.get(resourceId);
    if (existingId) {
      const existing = this.leases.get(existingId);
      if (existing && this.refreshState(existing) === 'active') {
        throw new LeaseError(
          'resource-locked',
          `resource "${resourceId}" is held by "${existing.context.holderId}"`,
        );
      }
    }
    const now = this.now();
    const context: LeaseContext = {
      leaseId: randomUUID(),
      resourceId,
      holderId,
      epoch: 1,
      generation: 0,
      acquiredAt: now,
      expiresAt: now + ttlMs,
      renewalCount: 0,
    };
    const record: LeaseRecord = { context, state: 'active', pendingHandover: null };
    this.leases.set(context.leaseId, record);
    this.leaseIdByResource.set(resourceId, context.leaseId);
    return { ...context };
  }

  /**
   * 动态续期：持有者在到期前延长租约。
   * 新的到期时间 = 当前时间 + extendMs，续期次数递增。
   */
  renew(leaseId: string, holderId: string, extendMs: number): LeaseContext {
    if (extendMs <= 0) throw new LeaseError('invalid-argument', 'extendMs must be positive');
    const record = this.getRecord(leaseId);
    const state = this.refreshState(record);
    if (state === 'expired') {
      throw new LeaseError('lease-expired', `lease ${leaseId} has expired`);
    }
    if (state === 'handover-pending') {
      throw new LeaseError('handover-pending', `lease ${leaseId} is frozen for handover`);
    }
    if (record.context.holderId !== holderId) {
      throw new LeaseError('not-holder', `node "${holderId}" does not hold lease ${leaseId}`);
    }
    record.context.expiresAt = this.now() + extendMs;
    record.context.renewalCount += 1;
    return { ...record.context };
  }

  /**
   * Handover 第一步：当前持有者主动发起移交。
   * 租约进入 handover-pending（冻结续期/释放以外的操作），
   * 协调中心签发携带剩余租约上下文的一次性交接包。
   */
  initiateHandover(leaseId: string, fromNodeId: string, toNodeId: string): HandoverPackage {
    const record = this.getRecord(leaseId);
    const state = this.refreshState(record);
    if (state === 'expired') {
      throw new LeaseError('lease-expired', `lease ${leaseId} has expired, cannot hand over`);
    }
    if (state !== 'active') {
      throw new LeaseError('invalid-state', `lease ${leaseId} is not active (state=${state})`);
    }
    if (record.context.holderId !== fromNodeId) {
      throw new LeaseError('not-holder', `node "${fromNodeId}" does not hold lease ${leaseId}`);
    }
    if (toNodeId === fromNodeId) {
      throw new LeaseError('invalid-argument', 'candidate must differ from current holder');
    }
    const now = this.now();
    const remainingTtlMs = record.context.expiresAt - now;
    if (remainingTtlMs <= 0) {
      throw new LeaseError('lease-expired', `lease ${leaseId} has no remaining ttl`);
    }
    const pkg: HandoverPackage = {
      leaseId,
      resourceId: record.context.resourceId,
      fromNodeId,
      toNodeId,
      epoch: record.context.epoch + 1,
      generation: record.context.generation + 1,
      remainingTtlMs,
      issuedAt: now,
      expiresAt: record.context.expiresAt,
      token: randomUUID(),
    };
    record.state = 'handover-pending';
    record.pendingHandover = pkg;
    return { ...pkg };
  }

  /**
   * Handover 第二步：候选人凭交接包接管租约。
   * 原样继承剩余租约上下文（到期时间不变），epoch 递增实现 fencing，
   * 全过程无需等待自然超时，切换耗时仅取决于一次进程内调用。
   */
  completeHandover(pkg: HandoverPackage): LeaseContext {
    const record = this.getRecord(pkg.leaseId);
    const state = this.refreshState(record);
    if (state === 'expired') {
      throw new LeaseError('lease-expired', `lease ${pkg.leaseId} expired before handover completed`);
    }
    if (state !== 'handover-pending' || !record.pendingHandover) {
      throw new LeaseError('invalid-state', `lease ${pkg.leaseId} has no pending handover`);
    }
    const pending = record.pendingHandover;
    if (pending.token !== pkg.token) {
      throw new LeaseError('invalid-token', 'handover token mismatch');
    }
    if (pending.toNodeId !== pkg.toNodeId) {
      throw new LeaseError('invalid-candidate', `node "${pkg.toNodeId}" is not the designated candidate`);
    }
    record.context = {
      ...record.context,
      holderId: pending.toNodeId,
      epoch: pending.epoch,
      generation: pending.generation,
      expiresAt: pending.expiresAt,
    };
    record.state = 'active';
    record.pendingHandover = null;
    const event: HandoverCompletedEvent = {
      leaseId: record.context.leaseId,
      resourceId: record.context.resourceId,
      fromNodeId: pending.fromNodeId,
      toNodeId: pending.toNodeId,
      epoch: pending.epoch,
      generation: pending.generation,
    };
    this.emit('handover-completed', event);
    return { ...record.context };
  }

  /** 候选人拒收或超时未接管时，持有者可中止移交并恢复租约 */
  abortHandover(leaseId: string, holderId: string): LeaseContext {
    const record = this.getRecord(leaseId);
    if (record.state !== 'handover-pending') {
      throw new LeaseError('invalid-state', `lease ${leaseId} has no pending handover`);
    }
    if (record.context.holderId !== holderId) {
      throw new LeaseError('not-holder', `node "${holderId}" does not hold lease ${leaseId}`);
    }
    record.state = 'active';
    record.pendingHandover = null;
    return { ...record.context };
  }

  /** 持有者主动释放租约 */
  release(leaseId: string, holderId: string): void {
    const record = this.getRecord(leaseId);
    if (record.context.holderId !== holderId) {
      throw new LeaseError('not-holder', `node "${holderId}" does not hold lease ${leaseId}`);
    }
    record.state = 'released';
    record.pendingHandover = null;
  }

  /**
   * fencing 校验：存储层/业务写入前调用，
   * 拒绝一切携带过期 epoch 的写入，防止脑裂双写。
   */
  assertFencing(resourceId: string, epoch: number): void {
    const leaseId = this.leaseIdByResource.get(resourceId);
    const record = leaseId ? this.leases.get(leaseId) : undefined;
    if (!record) throw new LeaseError('no-lease', `no lease for resource "${resourceId}"`);
    const state = this.refreshState(record);
    if (state !== 'active' && state !== 'handover-pending') {
      throw new LeaseError('lease-inactive', `lease for "${resourceId}" is ${state}`);
    }
    if (epoch < record.context.epoch) {
      throw new LeaseError(
        'fencing-token-stale',
        `stale epoch ${epoch}, current epoch is ${record.context.epoch}`,
      );
    }
  }

  /** 查询某资源当前租约快照（无则返回 null） */
  getLease(resourceId: string): (LeaseContext & { state: LeaseState }) | null {
    const leaseId = this.leaseIdByResource.get(resourceId);
    const record = leaseId ? this.leases.get(leaseId) : undefined;
    if (!record) return null;
    return { ...record.context, state: this.refreshState(record) };
  }

  private getRecord(leaseId: string): LeaseRecord {
    const record = this.leases.get(leaseId);
    if (!record) throw new LeaseError('no-lease', `unknown lease ${leaseId}`);
    return record;
  }

  /** 惰性过期判定：到期即落为 expired */
  private refreshState(record: LeaseRecord): LeaseState {
    if (
      (record.state === 'active' || record.state === 'handover-pending') &&
      this.now() >= record.context.expiresAt
    ) {
      record.state = 'expired';
      record.pendingHandover = null;
    }
    return record.state;
  }
}
