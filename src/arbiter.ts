/**
 * 租约仲裁器：持有主租约状态与内部互斥锁，
 * 所有状态变迁同步写入审计追踪器。
 *
 * 关键设计：LEASE_REQUESTED 在进入临界区【之前】同步记录，
 * 因此即使两个请求落在同一物理毫秒，审计流水仍能
 * 精确还原仲裁器观察到的到达次序。
 */
import { AuditTrail } from './audit-trail.ts';
import { LamportClock, type LogicalTimestamp } from './logical-clock.ts';
import type {
  AuditEvent,
  LeaseGrantedDetails,
  LeaseRejectedDetails,
  RejectReason,
} from './events.ts';

export interface LeaseHandle {
  leaseId: string;
  holderId: string;
  fencingToken: number;
  expiresAtMs: number;
}

export type AcquireResult =
  | { ok: true; handle: LeaseHandle; grantEvent: AuditEvent }
  | { ok: false; reason: RejectReason; rejectEvent: AuditEvent };

export interface ArbiterHooks {
  /**
   * 临界区进入后、状态判定前调用。
   * 测试可借此撑开竞态窗口，强制两个请求交错执行。
   */
  afterLockAcquired?: (operation: string) => void | Promise<void>;
}

export class LeaseArbiter {
  readonly trail: AuditTrail;
  private readonly clock = new LamportClock('arbiter');
  private readonly now: () => number;
  private readonly hooks: ArbiterHooks;

  private lockState: 'IDLE' | 'LOCKED' = 'IDLE';
  private mutex: Promise<void> = Promise.resolve();
  private holder: LeaseHandle | null = null;
  private nextFencingToken = 1;

  constructor(options: { now?: () => number; hooks?: ArbiterHooks; trail?: AuditTrail } = {}) {
    this.now = options.now ?? Date.now;
    this.hooks = options.hooks ?? {};
    this.trail = options.trail ?? new AuditTrail(this.now);
  }

  /** 当前持有者（只读快照） */
  currentHolder(): LeaseHandle | null {
    return this.holder ? { ...this.holder } : null;
  }

  /**
   * 申请租约。到达即记录 LEASE_REQUESTED（同步、无 await），
   * 然后排队进入仲裁器临界区做授予/拒绝判定。
   */
  async acquire(
    nodeId: string,
    leaseId: string,
    ttlMs: number,
    requesterClock?: LogicalTimestamp,
  ): Promise<AcquireResult> {
    const reqClock = requesterClock ?? this.clock.tick();
    const arrivalClock = this.clock.observe(reqClock);
    const requestEvent = this.trail.record({
      kind: 'LEASE_REQUESTED',
      leaseId,
      actorId: nodeId,
      clock: arrivalClock,
      details: { requesterClock: reqClock, ttlMs },
    });

    return this.enqueue(`acquire:${leaseId}`, requestEvent, async () => {
      const nowMs = this.now();
      if (this.holder !== null && this.holder.expiresAtMs > nowMs) {
        // —— 竞态冲突点：租约已被持有，检测到争抢 ——
        const contentionEvent = this.trail.record({
          kind: 'CONTENTION_DETECTED',
          leaseId,
          actorId: 'arbiter',
          clock: this.clock.tick(),
          causedBy: [requestEvent.seq],
          details: {
            winnerRequestSeq: this.holderRequestSeq,
            winnerNodeId: this.holder.holderId,
            loserRequestSeq: requestEvent.seq,
            loserNodeId: nodeId,
            arbiterLockState: this.lockState,
            sameMillisecond: false, // 由调用方上下文补充，见下
          },
        });
        const details = contentionEvent.details as {
          winnerRequestSeq: number;
          sameMillisecond: boolean;
        };
        details.winnerRequestSeq = this.holderRequestSeq;
        const winnerReq = this.trail.bySeq(this.holderRequestSeq);
        details.sameMillisecond = winnerReq.wallTimeMs === requestEvent.wallTimeMs;

        const rejectDetails: LeaseRejectedDetails = {
          reason: 'LEASE_HELD',
          currentHolderId: this.holder.holderId,
          currentFencingToken: this.holder.fencingToken,
        };
        const rejectEvent = this.trail.record({
          kind: 'LEASE_REJECTED',
          leaseId,
          actorId: nodeId,
          clock: this.clock.tick(),
          causedBy: [contentionEvent.seq, requestEvent.seq],
          details: rejectDetails,
        });
        return { ok: false, reason: 'LEASE_HELD', rejectEvent };
      }

      // 授予：递增 fencing token（脑裂防护）
      const handle: LeaseHandle = {
        leaseId,
        holderId: nodeId,
        fencingToken: this.nextFencingToken++,
        expiresAtMs: nowMs + ttlMs,
      };
      this.holder = handle;
      this.holderRequestSeq = requestEvent.seq;
      const grantDetails: LeaseGrantedDetails = {
        holderId: nodeId,
        fencingToken: handle.fencingToken,
        expiresAtMs: handle.expiresAtMs,
      };
      const grantEvent = this.trail.record({
        kind: 'LEASE_GRANTED',
        leaseId,
        actorId: nodeId,
        clock: this.clock.tick(),
        causedBy: [requestEvent.seq],
        details: grantDetails,
      });
      return { ok: true, handle: { ...handle }, grantEvent };
    });
  }

  private holderRequestSeq = 0;

  /** 续期：仅当前持有者且 fencing token 匹配时成功 */
  async renew(
    nodeId: string,
    leaseId: string,
    fencingToken: number,
    ttlMs: number,
  ): Promise<{ ok: boolean; reason?: RejectReason }> {
    return this.enqueue(`renew:${leaseId}`, null, async () => {
      const nowMs = this.now();
      let reason: RejectReason | null = null;
      if (this.holder === null || this.holder.leaseId !== leaseId || this.holder.expiresAtMs <= nowMs) {
        reason = 'LEASE_EXPIRED';
      } else if (this.holder.holderId !== nodeId) {
        reason = 'NOT_HOLDER';
      } else if (this.holder.fencingToken !== fencingToken) {
        reason = 'FENCING_TOKEN_STALE';
      }
      if (reason !== null) {
        this.trail.record({
          kind: 'LEASE_REJECTED',
          leaseId,
          actorId: nodeId,
          clock: this.clock.tick(),
          details: { reason, operation: 'renew' } satisfies LeaseRejectedDetails & { operation: string },
        });
        return { ok: false, reason };
      }
      this.holder!.expiresAtMs = nowMs + ttlMs;
      this.trail.record({
        kind: 'LEASE_RENEWED',
        leaseId,
        actorId: nodeId,
        clock: this.clock.tick(),
        details: { fencingToken, expiresAtMs: this.holder!.expiresAtMs },
      });
      return { ok: true };
    });
  }

  /** 移交：当前持有者主动把租约转给另一节点 */
  async transfer(
    fromNodeId: string,
    toNodeId: string,
    leaseId: string,
    fencingToken: number,
    ttlMs: number,
  ): Promise<{ ok: boolean; reason?: RejectReason }> {
    return this.enqueue(`transfer:${leaseId}`, null, async () => {
      if (
        this.holder === null ||
        this.holder.holderId !== fromNodeId ||
        this.holder.fencingToken !== fencingToken
      ) {
        const reason: RejectReason = this.holder === null ? 'LEASE_EXPIRED' : 'NOT_HOLDER';
        this.trail.record({
          kind: 'LEASE_REJECTED',
          leaseId,
          actorId: fromNodeId,
          clock: this.clock.tick(),
          details: { reason, operation: 'transfer' },
        });
        return { ok: false, reason };
      }
      this.holder = {
        leaseId,
        holderId: toNodeId,
        fencingToken: this.nextFencingToken++,
        expiresAtMs: this.now() + ttlMs,
      };
      this.trail.record({
        kind: 'LEASE_TRANSFERRED',
        leaseId,
        actorId: fromNodeId,
        clock: this.clock.tick(),
        details: {
          fromNodeId,
          toNodeId,
          fencingToken: this.holder.fencingToken,
          expiresAtMs: this.holder.expiresAtMs,
        },
      });
      return { ok: true };
    });
  }

  /** 超时：租约到期失效（由调度器/测试在到期后调用） */
  async expire(leaseId: string): Promise<void> {
    return this.enqueue(`expire:${leaseId}`, null, async () => {
      if (this.holder === null || this.holder.leaseId !== leaseId) return;
      const expired = this.holder;
      this.holder = null;
      this.trail.record({
        kind: 'LEASE_EXPIRED',
        leaseId,
        actorId: 'arbiter',
        clock: this.clock.tick(),
        details: {
          holderId: expired.holderId,
          fencingToken: expired.fencingToken,
          expiredAtMs: expired.expiresAtMs,
        },
      });
    });
  }

  /**
   * 互斥临界区：串行化所有状态判定，
   * 并把锁的获取/释放写入审计流水。
   */
  private enqueue<T>(
    operation: string,
    causedBy: AuditEvent | null,
    critical: () => Promise<T>,
  ): Promise<T> {
    const run = this.mutex.then(async () => {
      const prev = this.lockState;
      this.lockState = 'LOCKED';
      this.trail.record({
        kind: 'ARBITER_LOCK_ACQUIRED',
        leaseId: operation.split(':')[1],
        actorId: 'arbiter',
        clock: this.clock.tick(),
        causedBy: causedBy ? [causedBy.seq] : [],
        details: { operation, from: prev, to: 'LOCKED' },
      });
      try {
        await this.hooks.afterLockAcquired?.(operation);
        return await critical();
      } finally {
        this.lockState = 'IDLE';
        this.trail.record({
          kind: 'ARBITER_LOCK_RELEASED',
          leaseId: operation.split(':')[1],
          actorId: 'arbiter',
          clock: this.clock.tick(),
          details: { operation, from: 'LOCKED', to: 'IDLE' },
        });
      }
    });
    this.mutex = run.then(
      () => undefined,
      () => undefined,
    );
    return run;
  }
}
