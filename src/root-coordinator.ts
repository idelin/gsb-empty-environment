import type { Clock } from "./clock.ts";
import { isValid, type RegionLease } from "./lease.ts";

export interface RootCoordinatorOptions {
  clock: Clock;
  /** 粗粒度租约周期，默认 60_000ms（1 分钟） */
  leaseTtlMs?: number;
}

export interface RootStats {
  /** 根协调器处理的消息总数（衡量中央负载的核心指标） */
  messagesProcessed: number;
  leasesIssued: number;
  renewals: number;
  revocations: number;
}

/**
 * 根协调器：只与少量区域网关通信，颁发长周期粗粒度租约。
 * 采用惰性过期（仅在网关来续租时检查），自身不跑任何周期任务，
 * 因此边缘节点的任何 churn 都不会传导到根协调器。
 */
export class RootCoordinator {
  static readonly DEFAULT_LEASE_TTL_MS = 60_000;

  readonly stats: RootStats = {
    messagesProcessed: 0,
    leasesIssued: 0,
    renewals: 0,
    revocations: 0,
  };

  private readonly clock: Clock;
  private readonly leaseTtlMs: number;
  private readonly leases = new Map<string, RegionLease>();
  private readonly messageTimestamps: number[] = [];
  private seq = 0;

  constructor(options: RootCoordinatorOptions) {
    this.clock = options.clock;
    this.leaseTtlMs = options.leaseTtlMs ?? RootCoordinator.DEFAULT_LEASE_TTL_MS;
  }

  private recordMessage(): void {
    this.stats.messagesProcessed++;
    this.messageTimestamps.push(this.clock.now());
  }

  /** 区域网关申请粗粒度租约。 */
  requestLease(regionId: string, quota: number): RegionLease {
    this.recordMessage();
    const now = this.clock.now();
    const lease: RegionLease = {
      leaseId: `root-lease-${++this.seq}`,
      regionId,
      quota,
      issuedAt: now,
      expiresAt: now + this.leaseTtlMs,
      epoch: 1,
    };
    this.leases.set(lease.leaseId, lease);
    this.stats.leasesIssued++;
    return { ...lease };
  }

  /** 区域网关续租。租约不存在/已过期/epoch 不匹配时返回 null，调用方需重新申请。 */
  renewLease(leaseId: string, epoch: number): RegionLease | null {
    this.recordMessage();
    const lease = this.leases.get(leaseId);
    if (!lease || lease.epoch !== epoch || !isValid(lease, this.clock.now())) {
      return null;
    }
    lease.epoch++;
    lease.expiresAt = this.clock.now() + this.leaseTtlMs;
    this.stats.renewals++;
    return { ...lease };
  }

  /** 区域网关主动释放租约。 */
  revokeLease(leaseId: string): boolean {
    this.recordMessage();
    const existed = this.leases.delete(leaseId);
    if (existed) this.stats.revocations++;
    return existed;
  }

  /** 当前仍有效的租约数（惰性剔除过期项，仅供观测）。 */
  activeLeaseCount(): number {
    const now = this.clock.now();
    let count = 0;
    for (const lease of this.leases.values()) {
      if (isValid(lease, now)) count++;
    }
    return count;
  }

  /** 持有有效租约的区域 ID 列表（用于验证根只与网关通信）。 */
  activeRegionIds(): string[] {
    const now = this.clock.now();
    const ids: string[] = [];
    for (const lease of this.leases.values()) {
      if (isValid(lease, now)) ids.push(lease.regionId);
    }
    return ids.sort();
  }

  /** 负载探针：统计时间窗口内根协调器处理的消息数。 */
  messagesInWindow(startMs: number, endMs: number): number {
    let count = 0;
    for (const ts of this.messageTimestamps) {
      if (ts >= startMs && ts < endMs) count++;
    }
    return count;
  }
}
