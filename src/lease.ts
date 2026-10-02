export type LeaseStatus = "active" | "expired" | "revoked";

/**
 * 分区租约：网关授予某节点对某 Key Range 的独占服务权。
 * epoch 为全局单调递增的 fencing token，用于防止过期持有者继续服务。
 */
export interface Lease {
  readonly leaseId: string;
  readonly partitionId: string;
  readonly holderId: string;
  /** 全局 fencing token，单调递增 */
  readonly epoch: number;
  /** 租约继承链：父分区租约的 epoch（初始分区为 null） */
  readonly parentEpoch: number | null;
  readonly grantedAt: number;
  readonly expiresAt: number;
  status: LeaseStatus;
}

export class LeaseManager {
  private epochCounter = 0;
  private leaseSeq = 0;
  private readonly leases = new Map<string, Lease>();
  private readonly now: () => number;

  constructor(now: () => number = Date.now) {
    this.now = now;
  }

  /** 授予新租约；parentLease 存在时记录继承关系 */
  grant(
    partitionId: string,
    holderId: string,
    ttlMs: number,
    parentLease: Lease | null = null,
  ): Lease {
    const lease: Lease = {
      leaseId: `lease-${++this.leaseSeq}`,
      partitionId,
      holderId,
      epoch: ++this.epochCounter,
      parentEpoch: parentLease ? parentLease.epoch : null,
      grantedAt: this.now(),
      expiresAt: this.now() + ttlMs,
      status: "active",
    };
    this.leases.set(lease.leaseId, lease);
    return lease;
  }

  revoke(lease: Lease): void {
    if (lease.status === "active") lease.status = "revoked";
  }

  /** 校验租约当前是否有效（含 fencing 检查） */
  isValid(lease: Lease): boolean {
    if (lease.status !== "active") return false;
    if (this.now() >= lease.expiresAt) {
      lease.status = "expired";
      return false;
    }
    return true;
  }
}
