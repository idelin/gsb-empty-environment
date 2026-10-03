import { SystemClock, type Clock } from "./clock.ts";
import type { MasterCoordinator } from "./master.ts";

export interface WorkerOptions {
  id: string;
  master: MasterCoordinator;
  clock?: Clock;
  /** 每次心跳批量续订的 token 数 */
  batchSize: number;
  /** 本地余量低于 batchSize * 该比例时触发异步续订，默认 0.2 */
  renewThresholdRatio?: number;
}

export interface WorkerStats {
  id: string;
  localTokens: number;
  served: number;
  rejected: number;
  heartbeats: number;
}

/**
 * 工作节点：本地先消耗预分配的 Local Token；
 * 余量低于阈值时在后台异步批量心跳续订，不为每个请求做一次 RPC。
 */
export class WorkerNode {
  readonly id: string;
  private readonly master: MasterCoordinator;
  private readonly clock: Clock;
  private readonly batchSize: number;
  private readonly renewThreshold: number;
  private localTokens = 0;
  private leaseExpiresAt = 0;
  private usedSinceBeat = 0;
  private inflight: Promise<void> | null = null;
  private renewSuppressed = false;
  private servedCount = 0;
  private rejectedCount = 0;
  private heartbeatCount = 0;

  constructor(options: WorkerOptions) {
    this.id = options.id;
    this.master = options.master;
    this.clock = options.clock ?? new SystemClock();
    if (options.batchSize <= 0) throw new Error("batchSize must be positive");
    this.batchSize = options.batchSize;
    this.renewThreshold = Math.floor(
      options.batchSize * (options.renewThresholdRatio ?? 0.2),
    );
    this.master.registerWorker(this.id);
  }

  /** 注册后首次心跳，拿到初始配额 */
  async start(): Promise<void> {
    await this.renew();
  }

  /** 非阻塞限流入口：本地有余量则立即放行，否则触发后台续订并拒绝 */
  tryAcquire(n = 1): boolean {
    if (this.clock.now() >= this.leaseExpiresAt) this.localTokens = 0;
    if (this.localTokens >= n) {
      this.localTokens -= n;
      this.usedSinceBeat += n;
      this.servedCount += n;
      this.maybeRenew();
      return true;
    }
    this.maybeRenew();
    this.rejectedCount += n;
    return false;
  }

  /** 阻塞式获取：本地耗尽时等待在途心跳到账后重试（租约过期场景最多两个往返） */
  async acquire(n = 1): Promise<boolean> {
    if (this.tryAcquire(n)) return true;
    for (let attempt = 0; attempt < 2; attempt += 1) {
      await this.flush();
      if (this.tryAcquire(n)) return true;
    }
    return false;
  }

  /** 周期性保活心跳：只上报消耗速率并刷新租约，不请求新额度（避免配额乒乓） */
  async keepalive(): Promise<void> {
    await this.renew(0);
  }

  /** 等待在途的异步续订完成（测试与演示用） */
  async flush(): Promise<void> {
    if (this.inflight) await this.inflight;
  }

  private maybeRenew(): void {
    if (this.inflight) return;
    const expired = this.clock.now() >= this.leaseExpiresAt;
    // 上一次续订颗粒无收（池子耗尽）时抑制重试风暴，等租约过期/窗口滑过再试
    if (expired || (this.localTokens <= this.renewThreshold && !this.renewSuppressed)) {
      this.inflight = this.renew().finally(() => {
        this.inflight = null;
      });
    }
  }

  private async renew(requested = this.batchSize): Promise<void> {
    const response = this.master.heartbeat({
      workerId: this.id,
      used: this.usedSinceBeat,
      requested,
    });
    this.heartbeatCount += 1;
    this.usedSinceBeat = 0;
    // 若 Master 期间从本节点回收了空闲额度，响应中的 allocation 会把本地余量夹紧
    this.localTokens = Math.min(this.localTokens + response.granted, response.allocation);
    this.leaseExpiresAt = response.expiresAt;
    if (requested > 0) this.renewSuppressed = response.granted === 0;
  }

  stats(): WorkerStats {
    return {
      id: this.id,
      localTokens: this.localTokens,
      served: this.servedCount,
      rejected: this.rejectedCount,
      heartbeats: this.heartbeatCount,
    };
  }
}
