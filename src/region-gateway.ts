import type { Clock, TimerHandle } from "./clock.ts";
import { isValid, type RegionLease, type SubLease } from "./lease.ts";
import type { RootCoordinator } from "./root-coordinator.ts";

export interface RegionGatewayOptions {
  clock: Clock;
  /** 向根协调器申请的粗粒度配额 */
  quota: number;
  /** 子租约周期，默认 5_000ms（5 秒） */
  subLeaseTtlMs?: number;
  /** 在根租约剩余生命周期的该比例处续租，默认 0.5 */
  renewAtFraction?: number;
}

export interface GatewayStats {
  /** 网关处理的边缘消息总数（衡量分流效果） */
  messagesProcessed: number;
  subLeasesIssued: number;
  subLeaseRenewals: number;
  subLeaseExpirations: number;
  subLeaseReleases: number;
}

/**
 * 区域网关：持有根协调器的长周期粗粒度租约，
 * 在本区域内独立颁发/续期/回收短周期细粒度子租约。
 * 边缘节点掉线时，子租约在本地超时回收，完全不通知根协调器。
 */
export class RegionGateway {
  static readonly DEFAULT_SUB_LEASE_TTL_MS = 5_000;

  readonly stats: GatewayStats = {
    messagesProcessed: 0,
    subLeasesIssued: 0,
    subLeaseRenewals: 0,
    subLeaseExpirations: 0,
    subLeaseReleases: 0,
  };

  private readonly clock: Clock;
  private readonly root: RootCoordinator;
  private readonly quota: number;
  private readonly subLeaseTtlMs: number;
  private readonly renewAtFraction: number;

  private rootLease: RegionLease | null = null;
  private renewTimer: TimerHandle | null = null;
  private readonly subLeases = new Map<string, SubLease>();
  private readonly expiryTimers = new Map<string, TimerHandle>();
  private seq = 0;
  private running = false;

  readonly regionId: string;

  constructor(
    regionId: string,
    root: RootCoordinator,
    options: RegionGatewayOptions,
  ) {
    this.regionId = regionId;
    this.root = root;
    this.clock = options.clock;
    this.quota = options.quota;
    this.subLeaseTtlMs = options.subLeaseTtlMs ?? RegionGateway.DEFAULT_SUB_LEASE_TTL_MS;
    this.renewAtFraction = options.renewAtFraction ?? 0.5;
  }

  start(): void {
    if (this.running) return;
    this.running = true;
    this.acquireRootLease();
  }

  stop(): void {
    this.running = false;
    if (this.renewTimer !== null) {
      this.clock.clearTimeout(this.renewTimer);
      this.renewTimer = null;
    }
    if (this.rootLease) {
      this.root.revokeLease(this.rootLease.leaseId);
      this.rootLease = null;
    }
    for (const workerId of [...this.subLeases.keys()]) {
      this.reclaimSubLease(workerId, "release");
    }
  }

  private acquireRootLease(): void {
    this.rootLease = this.root.requestLease(this.regionId, this.quota);
    this.scheduleRootRenewal();
  }

  private scheduleRootRenewal(): void {
    if (!this.running || !this.rootLease) return;
    const remaining = this.rootLease.expiresAt - this.clock.now();
    const delay = Math.max(1, Math.floor(remaining * this.renewAtFraction));
    this.renewTimer = this.clock.setTimeout(() => this.renewRootLease(), delay);
  }

  private renewRootLease(): void {
    if (!this.running || !this.rootLease) return;
    const renewed = this.root.renewLease(this.rootLease.leaseId, this.rootLease.epoch);
    if (renewed) {
      this.rootLease = renewed;
      this.scheduleRootRenewal();
    } else {
      // 根租约失效：重新申请（期间不再颁发新子租约，已有子租约自然到期）
      this.acquireRootLease();
    }
  }

  private rootLeaseValid(): boolean {
    return this.rootLease !== null && isValid(this.rootLease, this.clock.now());
  }

  /** 工作节点申请子租约。配额不足或根租约失效时返回 null。 */
  requestSubLease(workerId: string, quota = 1): SubLease | null {
    this.stats.messagesProcessed++;
    if (!this.rootLeaseValid()) return null;
    const existing = this.subLeases.get(workerId);
    if (existing) return { ...existing };
    if (this.usedQuota() + quota > this.rootLease!.quota) return null;

    const now = this.clock.now();
    const subLease: SubLease = {
      subLeaseId: `${this.regionId}-sub-${++this.seq}`,
      workerId,
      regionId: this.regionId,
      quota,
      issuedAt: now,
      expiresAt: now + this.subLeaseTtlMs,
      epoch: 1,
    };
    this.subLeases.set(workerId, subLease);
    this.armExpiryTimer(workerId, this.subLeaseTtlMs);
    this.stats.subLeasesIssued++;
    return { ...subLease };
  }

  /** 工作节点心跳续租。 */
  renewSubLease(workerId: string, epoch: number): SubLease | null {
    this.stats.messagesProcessed++;
    const subLease = this.subLeases.get(workerId);
    if (!subLease || subLease.epoch !== epoch || !this.rootLeaseValid()) {
      return null;
    }
    subLease.epoch++;
    subLease.expiresAt = this.clock.now() + this.subLeaseTtlMs;
    this.armExpiryTimer(workerId, this.subLeaseTtlMs);
    this.stats.subLeaseRenewals++;
    return { ...subLease };
  }

  /** 工作节点优雅释放子租约。 */
  releaseSubLease(workerId: string): boolean {
    this.stats.messagesProcessed++;
    return this.reclaimSubLease(workerId, "release");
  }

  /** 子租约到期回收：纯本地操作，不触碰根协调器。 */
  private onSubLeaseExpired(workerId: string): void {
    const subLease = this.subLeases.get(workerId);
    if (!subLease) return;
    const now = this.clock.now();
    if (subLease.expiresAt > now) {
      // 续租与到期竞态：重新武装剩余时间
      this.armExpiryTimer(workerId, subLease.expiresAt - now);
      return;
    }
    this.reclaimSubLease(workerId, "expire");
  }

  private reclaimSubLease(workerId: string, reason: "expire" | "release"): boolean {
    const existed = this.subLeases.delete(workerId);
    const timer = this.expiryTimers.get(workerId);
    if (timer !== undefined) {
      this.clock.clearTimeout(timer);
      this.expiryTimers.delete(workerId);
    }
    if (existed) {
      if (reason === "expire") this.stats.subLeaseExpirations++;
      else this.stats.subLeaseReleases++;
    }
    return existed;
  }

  private armExpiryTimer(workerId: string, delayMs: number): void {
    const old = this.expiryTimers.get(workerId);
    if (old !== undefined) this.clock.clearTimeout(old);
    this.expiryTimers.set(
      workerId,
      this.clock.setTimeout(() => this.onSubLeaseExpired(workerId), Math.max(1, delayMs)),
    );
  }

  usedQuota(): number {
    let used = 0;
    for (const subLease of this.subLeases.values()) used += subLease.quota;
    return used;
  }

  availableQuota(): number {
    if (!this.rootLeaseValid()) return 0;
    return this.rootLease!.quota - this.usedQuota();
  }

  activeSubLeaseCount(): number {
    return this.subLeases.size;
  }

  hasSubLease(workerId: string): boolean {
    return this.subLeases.has(workerId);
  }

  currentRootLease(): RegionLease | null {
    return this.rootLease ? { ...this.rootLease } : null;
  }
}
