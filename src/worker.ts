import type { Clock } from "./clock.ts";
import { SystemClock } from "./clock.ts";
import type { HeartbeatResponse, InMemoryTransport } from "./transport.ts";

export interface WorkerOptions {
  id: string;
  transport: InMemoryTransport;
  /** 每次心跳批量申请的 token 数（预分配批量大小） */
  batchSize: number;
  /** 本地余额低于该阈值时触发异步批量续订 */
  renewThreshold: number;
  clock?: Clock;
}

/**
 * 工作节点：本地优先消耗预分配的 Local Token，
 * 余额低于阈值时异步批量心跳续订，避免每个请求一次 RPC。
 *
 * 一致性模型：心跳响应中的 allocated 是 Master 认定的本窗口
 * 权威配额，Worker 用 allocated - usedTotal 重算本地余额，
 * 因此被调拨走的额度会在下一次心跳时自动收敛。
 */
export class WorkerNode {
  readonly id: string;
  private readonly transport: InMemoryTransport;
  private readonly clock: Clock;
  private readonly batchSize: number;
  private readonly renewThreshold: number;

  private tokens = 0;
  private used = 0;
  private allocatedNow = 0;
  private leaseExpiresAt = Number.POSITIVE_INFINITY;
  private windowStart = -1;
  private inflight: Promise<void> | null = null;

  constructor(opts: WorkerOptions) {
    this.id = opts.id;
    this.transport = opts.transport;
    this.clock = opts.clock ?? new SystemClock();
    this.batchSize = opts.batchSize;
    this.renewThreshold = opts.renewThreshold;
  }

  get localTokens(): number {
    return this.tokens;
  }

  get usedTotal(): number {
    return this.used;
  }

  get allocated(): number {
    return this.allocatedNow;
  }

  get renewalInFlight(): boolean {
    return this.inflight !== null;
  }

  /** 本地扣减 token；余额不足时触发异步续订并返回 false（快速失败） */
  tryConsume(n = 1): boolean {
    this.expireIfNeeded();
    if (this.tokens < n) {
      this.renewAsync();
      return false;
    }
    this.tokens -= n;
    this.used += n;
    if (this.tokens <= this.renewThreshold) {
      this.renewAsync();
    }
    return true;
  }

  /** 强制一次批量心跳（注册/续订），等待完成 */
  async sync(): Promise<void> {
    const hadInflight = this.inflight !== null;
    if (hadInflight) await this.inflight;
    // 刚等待的续订若已覆盖当前租约则无需重复 RPC；
    // 否则（无 inflight，或 inflight 属于已过期窗口）强制一次批量心跳
    if (!hadInflight || this.clock.now() >= this.leaseExpiresAt) {
      await this.round(this.batchSize);
    }
  }

  /** 纯用量上报（requested=0），不申请新配额 */
  async report(): Promise<void> {
    if (this.inflight) await this.inflight;
    await this.round(0);
  }

  /** 等待已触发的异步续订完成 */
  async settle(): Promise<void> {
    await this.inflight;
  }

  private expireIfNeeded(): void {
    if (this.clock.now() >= this.leaseExpiresAt) {
      // 租约随窗口结束而失效，本地余额与用量计数一并作废
      this.tokens = 0;
      this.used = 0;
    }
  }

  private renewAsync(): void {
    if (this.inflight) return;
    this.inflight = this.round(this.batchSize).finally(() => {
      this.inflight = null;
    });
  }

  private async round(requested: number): Promise<void> {
    this.expireIfNeeded();
    const res = await this.transport.heartbeat({
      workerId: this.id,
      usedTotal: this.used,
      requested,
    });
    this.apply(res);
  }

  private apply(res: HeartbeatResponse): void {
    if (res.windowStart !== this.windowStart) {
      this.windowStart = res.windowStart;
      this.used = 0;
    }
    this.allocatedNow = res.allocated;
    this.leaseExpiresAt = res.expiresAt;
    this.tokens = Math.max(0, res.allocated - this.used);
  }
}
