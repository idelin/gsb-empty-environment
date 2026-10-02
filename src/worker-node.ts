import type { Clock } from "./clock.ts";
import type { RegionGateway } from "./region-gateway.ts";
import type { SubLease } from "./types.ts";

/**
 * 边缘工作节点：只与本区域网关通信，持有短周期子租约。
 * 掉线（drop）后停止续租，其子租约在网关侧自然超时释放。
 */
export class WorkerNode {
  readonly workerId: string;
  private readonly gateway: RegionGateway;
  private readonly clock: Clock;
  private readonly quotaNeed: number;
  private subLease: SubLease | null = null;
  private online = true;

  constructor(workerId: string, gateway: RegionGateway, clock: Clock, quotaNeed = 1) {
    this.workerId = workerId;
    this.gateway = gateway;
    this.clock = clock;
    this.quotaNeed = quotaNeed;
  }

  /** 每个仿真节拍调用：无租约则申请，过半则续租 */
  tick(): void {
    if (!this.online) return;
    const now = this.clock.now();
    if (this.subLease && this.subLease.expiresAt <= now) {
      this.subLease = null;
    }
    if (!this.subLease) {
      const granted = this.gateway.requestSubLease(this.workerId, this.quotaNeed);
      if (granted.ok) this.subLease = granted.subLease;
      return;
    }
    const remaining = this.subLease.expiresAt - now;
    if (remaining <= this.gateway.subLeaseTtlMs / 2) {
      const renewed = this.gateway.renewSubLease(this.subLease.subLeaseId, this.subLease.version);
      this.subLease = renewed.ok ? renewed.subLease : null;
    }
  }

  /** 模拟掉线：停止一切续租行为，本地句柄作废 */
  drop(): void {
    this.online = false;
    this.subLease = null;
  }

  isOnline(): boolean {
    return this.online;
  }

  hasValidSubLease(): boolean {
    return this.subLease !== null && this.subLease.expiresAt > this.clock.now();
  }
}
