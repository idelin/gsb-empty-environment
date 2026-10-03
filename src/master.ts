import { SystemClock, type Clock } from "./clock.ts";
import { SlidingWindowCounter } from "./slidingWindow.ts";

export interface HeartbeatRequest {
  workerId: string;
  /** 自上一次心跳以来本地已消耗的 token 数（批量上报） */
  used: number;
  /** 希望为下一个窗口续订的 token 批量大小 */
  requested: number;
}

export interface HeartbeatResponse {
  workerId: string;
  /** 本次心跳新授予的 token 数 */
  granted: number;
  /** 授予后该 worker 的未消耗租约总额（含本地尚未消耗的） */
  allocation: number;
  /** 租约过期时间戳（ms） */
  expiresAt: number;
}

export interface Reallocation {
  from: string;
  to: string;
  amount: number;
  at: number;
}

interface WorkerLease {
  id: string;
  /** 已租出且尚未上报消耗的 token 数 */
  allocation: number;
  expiresAt: number;
  /** 最近一个滑动窗口内上报的消耗速率 */
  usage: SlidingWindowCounter;
}

export interface WorkerSnapshot {
  id: string;
  allocation: number;
  usageInWindow: number;
  leaseExpiresInMs: number;
}

export interface PoolSnapshot {
  totalQps: number;
  consumedInWindow: number;
  available: number;
  workers: WorkerSnapshot[];
}

/**
 * 中心协调器：持有全局限流池（totalQps / windowMs），
 * 通过心跳租约把配额批量预分配给各 Worker，并从空闲 Worker 回收额度再调拨。
 *
 * 不变式（任意时刻）：consumedInWindow + Σ allocation ≤ totalQps，
 * 因此任意滑动窗口内全集群实际放行的请求数 ≤ totalQps。
 */
export class MasterCoordinator {
  readonly totalQps: number;
  readonly windowMs: number;
  readonly leaseTtlMs: number;
  private readonly clock: Clock;
  private readonly leases = new Map<string, WorkerLease>();
  private readonly consumed: SlidingWindowCounter;
  /** 收到的 heartbeat RPC 总次数（用于衡量批量化效果） */
  rpcCount = 0;
  readonly reallocations: Reallocation[] = [];

  constructor(options: {
    totalQps: number;
    windowMs?: number;
    leaseTtlMs?: number;
    clock?: Clock;
  }) {
    if (options.totalQps <= 0) throw new Error("totalQps must be positive");
    this.totalQps = options.totalQps;
    this.windowMs = options.windowMs ?? 1000;
    this.leaseTtlMs = options.leaseTtlMs ?? this.windowMs;
    this.clock = options.clock ?? new SystemClock();
    this.consumed = new SlidingWindowCounter(this.windowMs, 10, this.clock);
  }

  registerWorker(workerId: string): void {
    if (this.leases.has(workerId)) {
      throw new Error(`worker ${workerId} already registered`);
    }
    this.leases.set(workerId, {
      id: workerId,
      allocation: 0,
      expiresAt: 0,
      usage: new SlidingWindowCounter(this.windowMs, 10, this.clock),
    });
  }

  heartbeat(request: HeartbeatRequest): HeartbeatResponse {
    const now = this.clock.now();
    this.rpcCount += 1;
    const lease = this.leases.get(request.workerId);
    if (!lease) throw new Error(`unknown worker ${request.workerId}`);

    this.reclaimExpired(now);

    lease.allocation = Math.max(0, lease.allocation - request.used);
    lease.usage.add(request.used);
    this.consumed.add(request.used);
    lease.expiresAt = now + this.leaseTtlMs;

    const wanted = Math.max(0, request.requested);
    let grant = Math.min(wanted, Math.max(0, this.available()));
    if (grant < wanted) {
      grant += this.reclaimIdle(wanted - grant, request.workerId, now);
    }
    lease.allocation += grant;

    return {
      workerId: lease.id,
      granted: grant,
      allocation: lease.allocation,
      expiresAt: lease.expiresAt,
    };
  }

  /** 当前池中还可分配的额度 = 总量 - 窗口内已消耗 - 已租出未消耗 */
  available(): number {
    this.reclaimExpired(this.clock.now());
    return this.totalQps - this.consumed.sum() - this.outstanding();
  }

  snapshot(): PoolSnapshot {
    const now = this.clock.now();
    this.reclaimExpired(now);
    return {
      totalQps: this.totalQps,
      consumedInWindow: this.consumed.sum(),
      available: this.available(),
      workers: [...this.leases.values()]
        .map((lease) => ({
          id: lease.id,
          allocation: lease.allocation,
          usageInWindow: lease.usage.sum(),
          leaseExpiresInMs: Math.max(0, lease.expiresAt - now),
        }))
        .sort((a, b) => a.id.localeCompare(b.id)),
    };
  }

  private outstanding(): number {
    let total = 0;
    for (const lease of this.leases.values()) total += lease.allocation;
    return total;
  }

  /** 租约过期且未续订的 worker，其未消耗额度自动归还池中 */
  private reclaimExpired(now: number): void {
    for (const lease of this.leases.values()) {
      if (lease.allocation > 0 && now >= lease.expiresAt) {
        lease.allocation = 0;
      }
    }
  }

  /**
   * 从其他 worker 回收“空闲额度”（租约中超出其近期上报消耗速率的部分），
   * 按空闲量从大到小调拨，最多回收 needed 个 token。
   */
  private reclaimIdle(needed: number, excludeId: string, now: number): number {
    const candidates = [...this.leases.values()]
      .filter((lease) => lease.id !== excludeId)
      .map((lease) => ({
        lease,
        idle: Math.max(0, lease.allocation - Math.ceil(lease.usage.sum())),
      }))
      .filter((entry) => entry.idle > 0)
      .sort(
        (a, b) => b.idle - a.idle || a.lease.id.localeCompare(b.lease.id),
      );

    let reclaimed = 0;
    for (const { lease, idle } of candidates) {
      if (reclaimed >= needed) break;
      const take = Math.min(idle, needed - reclaimed);
      lease.allocation -= take;
      reclaimed += take;
      this.reallocations.push({ from: lease.id, to: excludeId, amount: take, at: now });
    }
    return reclaimed;
  }
}
