import type { Clock, TimerHandle } from "./clock.ts";
import type { SubLease } from "./lease.ts";
import type { RegionGateway } from "./region-gateway.ts";

export interface WorkerNodeOptions {
  clock: Clock;
  /** 心跳间隔，默认 2_000ms（小于 5s 子租约 TTL） */
  heartbeatIntervalMs?: number;
  /** 申请的配额单位，默认 1 */
  quota?: number;
}

/**
 * 边缘工作节点：只与本区域网关通信，对根协调器零感知。
 * disconnect() 模拟崩溃掉线：不打任何招呼，子租约由网关超时回收。
 */
export class WorkerNode {
  static readonly DEFAULT_HEARTBEAT_INTERVAL_MS = 2_000;

  private readonly clock: Clock;
  private readonly gateway: RegionGateway;
  private readonly heartbeatIntervalMs: number;
  private readonly quota: number;

  private subLease: SubLease | null = null;
  private heartbeatTimer: TimerHandle | null = null;
  private connected = false;

  readonly workerId: string;

  constructor(
    workerId: string,
    gateway: RegionGateway,
    options: WorkerNodeOptions,
  ) {
    this.workerId = workerId;
    this.gateway = gateway;
    this.clock = options.clock;
    this.heartbeatIntervalMs =
      options.heartbeatIntervalMs ?? WorkerNode.DEFAULT_HEARTBEAT_INTERVAL_MS;
    this.quota = options.quota ?? 1;
  }

  /** 上线并申请子租约。 */
  connect(): boolean {
    const lease = this.gateway.requestSubLease(this.workerId, this.quota);
    if (!lease) return false;
    this.subLease = lease;
    this.connected = true;
    this.scheduleHeartbeat();
    return true;
  }

  private scheduleHeartbeat(): void {
    this.heartbeatTimer = this.clock.setTimeout(
      () => this.heartbeat(),
      this.heartbeatIntervalMs,
    );
  }

  private heartbeat(): void {
    if (!this.connected) return;
    if (this.subLease) {
      const renewed = this.gateway.renewSubLease(this.workerId, this.subLease.epoch);
      if (renewed) {
        this.subLease = renewed;
        this.scheduleHeartbeat();
        return;
      }
      this.subLease = null;
    }
    // 子租约丢失：尝试重新申请，失败则下个心跳周期重试
    const lease = this.gateway.requestSubLease(this.workerId, this.quota);
    if (lease) this.subLease = lease;
    this.scheduleHeartbeat();
  }

  /** 模拟崩溃/掉线：停止心跳，不发送任何释放消息。 */
  disconnect(): void {
    this.connected = false;
    this.subLease = null;
    if (this.heartbeatTimer !== null) {
      this.clock.clearTimeout(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
  }

  /** 优雅下线：主动释放子租约。 */
  stop(): void {
    if (this.connected && this.subLease) {
      this.gateway.releaseSubLease(this.workerId);
    }
    this.disconnect();
  }

  isConnected(): boolean {
    return this.connected;
  }

  hasActiveSubLease(): boolean {
    return this.connected && this.subLease !== null;
  }
}
