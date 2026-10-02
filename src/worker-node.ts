import type { Clock } from "./clock.ts";
import { systemClock } from "./clock.ts";
import type { ConfigCenter, ConfigSubscriber } from "./config-center.ts";
import type { ClusterConfig, Lease } from "./lease.ts";

export class NoValidLeaseError extends Error {
  constructor(nodeId: string) {
    super(`node ${nodeId} holds no valid config lease; refusing traffic`);
    this.name = "NoValidLeaseError";
  }
}

export interface WorkerNodeOptions {
  /** 距到期还有多久时发起续租（毫秒）。 */
  renewalMarginMs: number;
  clock?: Clock;
}

export interface ServeResponse {
  nodeId: string;
  configVersion: number;
  dbPassword: string;
}

/**
 * 工作节点：向配置中心租用配置版本，只在持有有效租约时对外接客。
 *
 * - 在线时：临期续租；续租被拒（旧版本/已撤销）立即停服。
 * - 脱网时：无法续租也收不到推送，旧租约 TTL 到期后自然失效，主动停止接收流量，
 *   防止使用过期配置操作脏数据。
 */
export class WorkerNode implements ConfigSubscriber {
  readonly nodeId: string;
  private readonly center: ConfigCenter;
  private readonly clock: Clock;
  private readonly renewalMarginMs: number;

  private config: ClusterConfig | null = null;
  private lease: Lease | null = null;
  private serving = false;
  private connected = false;
  private maintainTimer: ReturnType<typeof setInterval> | null = null;

  constructor(nodeId: string, center: ConfigCenter, options: WorkerNodeOptions) {
    this.nodeId = nodeId;
    this.center = center;
    this.clock = options.clock ?? systemClock;
    this.renewalMarginMs = options.renewalMarginMs;
  }

  /** 上线：注册推送通道并租用当前配置。 */
  start(): void {
    this.connected = true;
    this.center.register(this);
    const { config, lease } = this.center.rent(this.nodeId);
    this.config = config;
    this.lease = lease;
    this.serving = true;
  }

  /** 下线：注销并释放本地状态。 */
  stop(): void {
    this.connected = false;
    this.center.unregister(this.nodeId);
    this.serving = false;
    this.config = null;
    this.lease = null;
    if (this.maintainTimer) {
      clearInterval(this.maintainTimer);
      this.maintainTimer = null;
    }
  }

  /** 模拟脱网：收不到推送、无法续租，但本地租约仍在 TTL 内。 */
  disconnect(): void {
    this.connected = false;
    this.center.unregister(this.nodeId);
  }

  reconnect(): void {
    if (this.serving) {
      this.connected = true;
      this.center.register(this);
      this.maintain();
    }
  }

  onConfigPush(config: ClusterConfig, lease: Lease): void {
    this.config = config;
    this.lease = lease;
    this.serving = true;
  }

  get isServing(): boolean {
    return this.serving && this.hasValidLease();
  }

  get currentConfigVersion(): number | null {
    return this.config?.version ?? null;
  }

  /** 本地租约校验：只看 TTL，不依赖中心可达。 */
  hasValidLease(): boolean {
    return this.lease !== null && this.clock.now() < this.lease.expiresAt;
  }

  /**
   * 对外接客。无有效租约时拒绝流量，绝不使用过期配置操作数据。
   */
  serve(requestId: string): ServeResponse {
    if (!this.serving || !this.hasValidLease()) {
      throw new NoValidLeaseError(this.nodeId);
    }
    void requestId;
    return {
      nodeId: this.nodeId,
      configVersion: this.config!.version,
      dbPassword: this.config!.dbPassword,
    };
  }

  /**
   * 租约维护（由定时器或测试手动触发）：
   * 1. 租约已自然到期 → 立即停止接客；
   * 2. 脱网 → 无法续租，等待 TTL 自然到期；
   * 3. 临期 → 向中心续租，被拒（版本过旧/已撤销）→ 立即停止接客。
   */
  maintain(): void {
    if (!this.lease) {
      this.serving = false;
      return;
    }
    const now = this.clock.now();
    if (now >= this.lease.expiresAt) {
      this.lease = null;
      this.serving = false;
      return;
    }
    if (!this.connected) return;
    if (now >= this.lease.expiresAt - this.renewalMarginMs) {
      const result = this.center.renewLease(this.lease.leaseId);
      if (result.ok) {
        this.lease = result.lease;
      } else {
        this.lease = null;
        this.serving = false;
      }
    }
  }

  /** 启动周期性租约维护（演示用；测试里手动调用 maintain 更确定）。 */
  startAutoMaintain(intervalMs: number): void {
    if (this.maintainTimer) return;
    this.maintainTimer = setInterval(() => this.maintain(), intervalMs);
    this.maintainTimer.unref?.();
  }
}
