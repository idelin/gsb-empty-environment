import type { Clock } from "./clock.ts";
import type { RootCoordinator } from "./root-coordinator.ts";
import type { RootLease, SubGrantResult, SubLease } from "./types.ts";

export interface RegionGatewayOptions {
  /** 子租约 TTL，默认 5_000ms（5 秒） */
  subLeaseTtlMs?: number;
  /** 向根中心申请的配额池大小 */
  quotaPool?: number;
  /** 根租约剩余多少比例时触发续租，默认 0.5（过半即续） */
  renewThreshold?: number;
}

/**
 * 区域网关：持有根租约，将配额细分为短周期子租约颁发给本区工作节点。
 * 边缘节点的申请、续租、掉线超时全部在本地消化，不触达根协调器。
 */
export class RegionGateway {
  readonly gatewayId: string;
  readonly subLeaseTtlMs: number;
  private readonly quotaPool: number;
  private readonly renewThreshold: number;
  private readonly root: RootCoordinator;
  private readonly clock: Clock;

  private rootLease: RootLease | null = null;
  private subLeases = new Map<string, SubLease>();
  private byWorker = new Map<string, string>();
  private seq = 0;
  private localGranted = 0;

  constructor(
    gatewayId: string,
    root: RootCoordinator,
    clock: Clock,
    options: RegionGatewayOptions = {},
  ) {
    this.gatewayId = gatewayId;
    this.root = root;
    this.clock = clock;
    this.subLeaseTtlMs = options.subLeaseTtlMs ?? 5_000;
    this.quotaPool = options.quotaPool ?? 10_000;
    this.renewThreshold = options.renewThreshold ?? 0.5;
  }

  /** 确保持有有效根租约；临近过期则续租。这是网关与根中心唯一的交互点。 */
  private ensureRootLease(): boolean {
    const now = this.clock.now();
    const ttl = this.root.leaseTtlMs;
    if (this.rootLease && this.rootLease.expiresAt > now) {
      const remaining = this.rootLease.expiresAt - now;
      if (remaining > ttl * (1 - this.renewThreshold)) return true;
      const renewed = this.root.renewLease(this.rootLease.leaseId, this.rootLease.version);
      if (renewed.ok) {
        this.rootLease = renewed.lease;
        return true;
      }
      this.rootLease = null;
    }
    const granted = this.root.requestLease(this.gatewayId, this.quotaPool);
    if (granted.ok) {
      this.rootLease = granted.lease;
      return true;
    }
    return false;
  }

  /** 工作节点申请细粒度子租约 */
  requestSubLease(workerId: string, quota: number): SubGrantResult {
    const now = this.clock.now();
    this.expireWorkerIfStale(workerId);

    const existingId = this.byWorker.get(workerId);
    if (existingId !== undefined) {
      const existing = this.subLeases.get(existingId);
      if (existing && existing.expiresAt > now) {
        return { ok: true, subLease: existing };
      }
    }

    if (!this.ensureRootLease()) {
      return { ok: false, reason: "root lease unavailable" };
    }
    if (!this.root.debitQuota(this.gatewayId, quota)) {
      return { ok: false, reason: "regional quota exhausted" };
    }

    const subLease: SubLease = {
      subLeaseId: `${this.gatewayId}-sub-${++this.seq}`,
      workerId,
      gatewayId: this.gatewayId,
      quota,
      issuedAt: now,
      expiresAt: now + this.subLeaseTtlMs,
      version: 1,
    };
    this.subLeases.set(subLease.subLeaseId, subLease);
    this.byWorker.set(workerId, subLease.subLeaseId);
    this.localGranted += quota;
    return { ok: true, subLease };
  }

  /** 工作节点续租子租约（本地操作，不触达根中心） */
  renewSubLease(subLeaseId: string, version: number): SubGrantResult {
    const now = this.clock.now();
    const subLease = this.subLeases.get(subLeaseId);
    if (!subLease) return { ok: false, reason: "unknown sub-lease" };
    if (subLease.expiresAt <= now) {
      this.reclaim(subLease);
      return { ok: false, reason: "sub-lease expired" };
    }
    if (subLease.version !== version) {
      return { ok: false, reason: "version mismatch" };
    }
    if (!this.ensureRootLease()) {
      return { ok: false, reason: "root lease unavailable" };
    }
    subLease.expiresAt = now + this.subLeaseTtlMs;
    subLease.version++;
    return { ok: true, subLease };
  }

  /** 工作节点主动释放子租约 */
  releaseSubLease(subLeaseId: string): boolean {
    const subLease = this.subLeases.get(subLeaseId);
    if (!subLease) return false;
    this.reclaim(subLease);
    return true;
  }

  /**
   * 本地清扫：过期子租约在网关侧超时释放，配额归还根租约池。
   * 全程无根中心通信 —— 边缘掉线对根负载零影响的关键路径。
   */
  sweep(): string[] {
    const now = this.clock.now();
    const expired: string[] = [];
    for (const subLease of this.subLeases.values()) {
      if (subLease.expiresAt <= now) expired.push(subLease.subLeaseId);
    }
    for (const subLeaseId of expired) {
      const subLease = this.subLeases.get(subLeaseId);
      if (subLease) this.reclaim(subLease);
    }
    return expired;
  }

  activeSubLeaseCount(): number {
    return this.subLeases.size;
  }

  localGrantedQuota(): number {
    return this.localGranted;
  }

  hasValidRootLease(): boolean {
    return this.rootLease !== null && this.rootLease.expiresAt > this.clock.now();
  }

  private expireWorkerIfStale(workerId: string): void {
    const subLeaseId = this.byWorker.get(workerId);
    if (subLeaseId === undefined) return;
    const subLease = this.subLeases.get(subLeaseId);
    if (subLease && subLease.expiresAt <= this.clock.now()) this.reclaim(subLease);
  }

  private reclaim(subLease: SubLease): void {
    this.subLeases.delete(subLease.subLeaseId);
    if (this.byWorker.get(subLease.workerId) === subLease.subLeaseId) {
      this.byWorker.delete(subLease.workerId);
    }
    this.localGranted -= subLease.quota;
    this.root.creditQuota(this.gatewayId, subLease.quota);
  }
}
