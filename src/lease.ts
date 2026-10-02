/**
 * 租约注册表：管理活跃租约，支持安全模式降级。
 * 进入安全模式后拒绝一切新租约，防止时钟混乱期间的逻辑灾难。
 */

export interface Lease {
  readonly id: string;
  readonly holder: string;
  readonly acquiredAt: number;
  readonly expiresAt: number | null;
}

export class SafeModeError extends Error {
  constructor() {
    super("LeaseRegistry is in SAFE MODE: new leases are rejected");
    this.name = "SafeModeError";
  }
}

let nextLeaseSeq = 0;

export class LeaseRegistry {
  private readonly leases = new Map<string, Lease>();
  private safeMode = false;

  get isSafeMode(): boolean {
    return this.safeMode;
  }

  get activeCount(): number {
    return this.leases.size;
  }

  activeLeases(): Lease[] {
    return [...this.leases.values()];
  }

  /** 申请租约；安全模式下立即拒绝。 */
  acquire(holder: string, now: number, ttlMs?: number): Lease {
    if (this.safeMode) {
      throw new SafeModeError();
    }
    const lease: Lease = {
      id: `lease-${++nextLeaseSeq}`,
      holder,
      acquiredAt: now,
      expiresAt: ttlMs === undefined ? null : now + ttlMs,
    };
    this.leases.set(lease.id, lease);
    return lease;
  }

  /** 正常释放单个租约。 */
  release(id: string): boolean {
    return this.leases.delete(id);
  }

  /** 紧急熔断：强制注销全部活跃租约，返回被注销的租约列表。 */
  revokeAll(): Lease[] {
    const revoked = this.activeLeases();
    this.leases.clear();
    return revoked;
  }

  /** 降级为安全模式。 */
  enterSafeMode(): void {
    this.safeMode = true;
  }

  /** 人工确认时钟恢复后退出安全模式。 */
  exitSafeMode(): void {
    this.safeMode = false;
  }
}
