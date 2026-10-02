import type { Clock } from './clock.ts';
import { LeaseHeldError, StaleLeaseCredentialError } from './errors.ts';

/** 法官颁发的租约凭证。epoch 为全局单调递增的 Fencing Token。 */
export interface LeaseGrant {
  holderId: string;
  epoch: number;
  issuedAt: number;
  expiresAt: number;
}

/**
 * 第三方法官（Witness）：部署在独立故障域的轻量仲裁者。
 * 任一时刻全局最多只有一份活跃租约；只有租约真正过期（TTL 耗尽且未续租）
 * 才会把新租约颁发给另一机房，从而杜绝双机房双写。
 */
export class Witness {
  private readonly clock: Clock;
  private current: LeaseGrant | null = null;
  private epochCounter = 0;

  constructor(clock: Clock) {
    this.clock = clock;
  }

  /** 当前仍活跃的租约（未过期），无则返回 null。 */
  activeLease(): LeaseGrant | null {
    if (this.current && this.current.expiresAt > this.clock.now()) {
      return { ...this.current };
    }
    return null;
  }

  /** 申请租约。已有他人持有活跃租约时拒绝；原租约过期后颁发给接管者，epoch 递增。 */
  requestLease(dcId: string, ttlMs: number): LeaseGrant {
    const now = this.clock.now();
    const active = this.activeLease();
    if (active && active.holderId !== dcId) {
      throw new LeaseHeldError(active.holderId, active.epoch, active.expiresAt);
    }
    this.epochCounter += 1;
    this.current = {
      holderId: dcId,
      epoch: this.epochCounter,
      issuedAt: now,
      expiresAt: now + ttlMs,
    };
    return { ...this.current };
  }

  /** 续租：凭证（holderId + epoch）必须仍是法官记录的最新凭证。 */
  renewLease(dcId: string, epoch: number, ttlMs: number): LeaseGrant {
    const now = this.clock.now();
    if (!this.current || this.current.holderId !== dcId || this.current.epoch !== epoch) {
      throw new StaleLeaseCredentialError(dcId, epoch);
    }
    this.current = { ...this.current, expiresAt: now + ttlMs };
    return { ...this.current };
  }

  /** 校验某凭证当前是否仍然合法（供恢复后的机房自检）。 */
  validate(dcId: string, epoch: number): boolean {
    const active = this.activeLease();
    return active !== null && active.holderId === dcId && active.epoch === epoch;
  }
}
