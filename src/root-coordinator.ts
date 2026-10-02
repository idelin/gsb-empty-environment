import type { Clock } from "./clock.ts";
import type { GrantResult, RootLease, RootMetrics } from "./types.ts";

export interface RootCoordinatorOptions {
  /** 根租约 TTL，默认 60_000ms（1 分钟） */
  leaseTtlMs?: number;
  /** 根中心可分配的总配额 */
  totalQuota?: number;
}

/**
 * 根协调器：只与少量区域网关通信，颁发长周期粗粒度租约。
 * 不感知任何边缘工作节点的存在。
 */
export class RootCoordinator {
  readonly leaseTtlMs: number;
  private totalQuota: number;
  private allocatedQuota = 0;
  private leases = new Map<string, RootLease>();
  private byGateway = new Map<string, string>();
  private seq = 0;
  private clock: Clock;
  private metrics: RootMetrics = {
    messagesHandled: 0,
    leaseOperations: 0,
    activeRootLeases: 0,
  };
  /** 通信对端记录：验收“根仅与 5 个网关维系租约通信” */
  private peers = new Set<string>();

  constructor(clock: Clock, options: RootCoordinatorOptions = {}) {
    this.clock = clock;
    this.leaseTtlMs = options.leaseTtlMs ?? 60_000;
    this.totalQuota = options.totalQuota ?? 1_000_000;
  }

  /** 网关申请（或重新申请）粗粒度租约 */
  requestLease(gatewayId: string, quota: number): GrantResult {
    this.metrics.messagesHandled++;
    this.metrics.leaseOperations++;
    this.peers.add(gatewayId);
    const now = this.clock.now();

    const existingId = this.byGateway.get(gatewayId);
    if (existingId !== undefined) {
      const existing = this.leases.get(existingId);
      if (existing && existing.expiresAt > now) {
        return { ok: true, lease: existing };
      }
      if (existing) this.reclaim(existing);
    }

    if (this.allocatedQuota + quota > this.totalQuota) {
      return { ok: false, reason: "root quota exhausted" };
    }

    const lease: RootLease = {
      leaseId: `root-lease-${++this.seq}`,
      gatewayId,
      quotaTotal: quota,
      quotaAvailable: quota,
      issuedAt: now,
      expiresAt: now + this.leaseTtlMs,
      version: 1,
    };
    this.leases.set(lease.leaseId, lease);
    this.byGateway.set(gatewayId, lease.leaseId);
    this.allocatedQuota += quota;
    this.syncActiveCount();
    return { ok: true, lease };
  }

  /** 网关续租：延长 1 个 TTL 周期 */
  renewLease(leaseId: string, version: number): GrantResult {
    this.metrics.messagesHandled++;
    this.metrics.leaseOperations++;
    const now = this.clock.now();
    const lease = this.leases.get(leaseId);
    if (!lease) return { ok: false, reason: "unknown lease" };
    this.peers.add(lease.gatewayId);
    if (lease.expiresAt <= now) {
      this.reclaim(lease);
      return { ok: false, reason: "lease expired" };
    }
    if (lease.version !== version) {
      return { ok: false, reason: "version mismatch" };
    }
    lease.expiresAt = now + this.leaseTtlMs;
    lease.version++;
    return { ok: true, lease };
  }

  /** 网关主动释放租约 */
  releaseLease(leaseId: string): boolean {
    this.metrics.messagesHandled++;
    this.metrics.leaseOperations++;
    const lease = this.leases.get(leaseId);
    if (!lease) return false;
    this.reclaim(lease);
    return true;
  }

  /** 周期清扫：回收过期网关租约。调用频率低（与网关数同阶）。 */
  sweep(): string[] {
    const now = this.clock.now();
    const expired: string[] = [];
    for (const lease of this.leases.values()) {
      if (lease.expiresAt <= now) expired.push(lease.leaseId);
    }
    for (const leaseId of expired) {
      const lease = this.leases.get(leaseId);
      if (lease) this.reclaim(lease);
    }
    if (expired.length > 0) this.metrics.leaseOperations += expired.length;
    return expired;
  }

  /** 网关代子租约扣减/归还配额（纯内存记账，不新增通信对端） */
  debitQuota(gatewayId: string, amount: number): boolean {
    const leaseId = this.byGateway.get(gatewayId);
    const lease = leaseId === undefined ? undefined : this.leases.get(leaseId);
    if (!lease || lease.expiresAt <= this.clock.now()) return false;
    if (lease.quotaAvailable < amount) return false;
    lease.quotaAvailable -= amount;
    return true;
  }

  creditQuota(gatewayId: string, amount: number): void {
    const leaseId = this.byGateway.get(gatewayId);
    const lease = leaseId === undefined ? undefined : this.leases.get(leaseId);
    if (!lease) return;
    lease.quotaAvailable = Math.min(lease.quotaTotal, lease.quotaAvailable + amount);
  }

  getMetrics(): RootMetrics {
    this.syncActiveCount();
    return { ...this.metrics };
  }

  /** 根中心通信对端集合（应只包含区域网关 ID） */
  getPeers(): string[] {
    return [...this.peers].sort();
  }

  activeLeaseCount(): number {
    this.syncActiveCount();
    return this.metrics.activeRootLeases;
  }

  private reclaim(lease: RootLease): void {
    this.leases.delete(lease.leaseId);
    if (this.byGateway.get(lease.gatewayId) === lease.leaseId) {
      this.byGateway.delete(lease.gatewayId);
    }
    this.allocatedQuota -= lease.quotaTotal;
    this.syncActiveCount();
  }

  private syncActiveCount(): void {
    this.metrics.activeRootLeases = this.leases.size;
  }
}
