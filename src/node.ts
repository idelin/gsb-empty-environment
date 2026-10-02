import { performance } from 'node:perf_hooks';
import { LeaseCoordinator } from './coordinator.ts';
import { LeaseError } from './types.ts';
import type { HandoverPackage, LeaseToken } from './types.ts';

export type NodeRole = 'idle' | 'primary' | 'standby';

export interface NodeOptions {
  /** 期望的租约 TTL，续期时会根据观测到的 RTT 动态调整。 */
  ttlMs?: number;
  /** 续期间隔 = TTL / renewFraction（默认 3，即 TTL 的 1/3 处续期）。 */
  renewFraction?: number;
  /** 是否自动启动续期循环（测试时可关闭，手动驱动）。 */
  autoRenew?: boolean;
}

export interface HandoverMetrics {
  /** 从冻结本地写入到候选人完成接管的业务切换耗时（ms）。 */
  switchoverMs: number;
  handoverId: string;
}

/**
 * 租约节点：可作为持有者（primary）或候选人（standby）。
 * - 持有期间按动态间隔自动续期；
 * - 计划停机时冻结本地写入，把剩余租约上下文打包移交给候选人；
 * - 候选人收到交接包后立即接管主角色，业务无中断。
 */
export class LeaseNode {
  readonly id: string;
  private readonly coordinator: LeaseCoordinator;
  private readonly ttlMs: number;
  private readonly renewFraction: number;
  private readonly autoRenew: boolean;

  private token: LeaseToken | undefined;
  private renewTimer: NodeJS.Timeout | undefined;
  private renewFailures = 0;
  private smoothedRttMs = 0;

  role: NodeRole = 'idle';
  /** 冻结期间写入进入待移交队列，随交接包交给下一任主。 */
  private frozen = false;
  private pendingWrites: string[] = [];
  /** 已应用到本地业务存储的写。 */
  readonly appliedWrites: string[] = [];
  /** 最近一次移交的度量。 */
  lastHandover: HandoverMetrics | undefined;

  constructor(id: string, coordinator: LeaseCoordinator, options: NodeOptions = {}) {
    this.id = id;
    this.coordinator = coordinator;
    this.ttlMs = options.ttlMs ?? 2_000;
    this.renewFraction = options.renewFraction ?? 3;
    this.autoRenew = options.autoRenew ?? true;
  }

  get isPrimary(): boolean {
    return this.role === 'primary';
  }

  get fencingToken(): number | undefined {
    return this.token?.fencingToken;
  }

  /** 竞选成为主节点。 */
  acquire(resourceId: string): void {
    this.token = this.coordinator.acquire(resourceId, this.id, this.ttlMs);
    this.role = 'primary';
    this.frozen = false;
    if (this.autoRenew) this.scheduleRenewal();
  }

  /**
   * 业务写入口。主角色且未冻结时立即落盘；
   * 移交冻结期间进入待移交队列，保证客户端零错误。
   */
  write(data: string): void {
    if (this.role !== 'primary') {
      throw new LeaseError('NOT_PRIMARY', `node ${this.id} is not primary`);
    }
    if (this.frozen) {
      this.pendingWrites.push(data);
      return;
    }
    this.applyWrite(data);
  }

  private applyWrite(data: string): void {
    if (!this.token) throw new LeaseError('NO_LEASE', 'no lease token');
    this.coordinator.validateWrite(this.token);
    this.appliedWrites.push(data);
  }

  /**
   * 滚动重启第一步：冻结本地写入。之后到达的写进入待移交队列，
   * 对客户端表现为短暂排队而非错误。
   */
  freeze(): void {
    if (this.role !== 'primary') {
      throw new LeaseError('NOT_PRIMARY', `node ${this.id} is not primary`);
    }
    this.frozen = true;
  }

  /**
   * 主动移交（优雅停机）：冻结本地写入 → 向协调中心登记交接 →
   * 把交接包（含剩余租约上下文与冻结期写队列）交给候选人 →
   * 候选人原子接管。全程同步完成，切换耗时仅毫秒级。
   */
  handoverTo(candidate: LeaseNode): HandoverMetrics {
    if (!this.token || this.role !== 'primary') {
      throw new LeaseError('NOT_PRIMARY', `node ${this.id} has no lease to hand over`);
    }
    const start = performance.now();
    // 1. 冻结本地写入，之后到达的写进入待移交队列。
    this.freeze();
    // 2. 向协调中心发出主动释放指令并指定接盘候选人。
    const pkg = this.coordinator.prepareHandover(this.token, candidate.id, {
      pendingWrites: [...this.pendingWrites],
    });
    this.pendingWrites = [];
    // 3. 交接包送达候选人，候选人无缝继承剩余租约上下文。
    candidate.receiveHandover(pkg);
    // 4. 本地退位：停止续期，转为 standby。
    this.stopRenewal();
    this.role = 'standby';
    this.token = undefined;
    const metrics: HandoverMetrics = {
      switchoverMs: performance.now() - start,
      handoverId: pkg.handoverId,
    };
    this.lastHandover = metrics;
    return metrics;
  }

  /** 候选人侧：收到交接包后立即接管主角色。 */
  receiveHandover(pkg: HandoverPackage): void {
    if (pkg.toCandidateId !== this.id) {
      throw new LeaseError('WRONG_CANDIDATE', `handover ${pkg.handoverId} is not for ${this.id}`);
    }
    this.token = this.coordinator.acceptHandover(pkg);
    this.role = 'primary';
    this.frozen = false;
    // 继承移交的业务上下文：回放冻结期间积压的写。
    const inherited = (pkg.context['pendingWrites'] as string[] | undefined) ?? [];
    for (const data of inherited) this.applyWrite(data);
    if (this.autoRenew) this.scheduleRenewal();
  }

  /**
   * 动态续期：基础间隔为 TTL 的 1/renewFraction，
   * 扣除平滑 RTT 并叠加抖动；连续失败时指数退避，
   * 网络抖动下既不过度请求也不临近过期才续。
   */
  renewalIntervalMs(): number {
    const base = this.ttlMs / this.renewFraction;
    const jitter = base * 0.1 * (Math.random() * 2 - 1);
    const healthy = Math.max(10, base - this.smoothedRttMs + jitter);
    return healthy * 2 ** this.renewFailures;
  }

  /** 执行一次续期（测试可直接调用）。 */
  renewOnce(): void {
    if (!this.token) return;
    const start = performance.now();
    try {
      this.token = this.coordinator.renew(this.token, this.ttlMs);
      const rtt = performance.now() - start;
      this.smoothedRttMs = this.smoothedRttMs === 0 ? rtt : 0.8 * this.smoothedRttMs + 0.2 * rtt;
      this.renewFailures = 0;
    } catch (err) {
      this.renewFailures += 1;
      if ((err as LeaseError).code === 'FENCING_VIOLATION' || (err as LeaseError).code === 'LEASE_NOT_FOUND') {
        // 租约已易主，停止续期并退位。
        this.stopRenewal();
        this.role = 'standby';
        this.token = undefined;
        return;
      }
      if (this.renewFailures >= 5) {
        this.stopRenewal();
        this.role = 'standby';
        this.token = undefined;
      }
    }
  }

  private scheduleRenewal(): void {
    this.stopRenewal();
    const tick = (): void => {
      this.renewOnce();
      if (this.role === 'primary') {
        this.renewTimer = setTimeout(tick, this.renewalIntervalMs());
        this.renewTimer.unref?.();
      }
    };
    this.renewTimer = setTimeout(tick, this.renewalIntervalMs());
    this.renewTimer.unref?.();
  }

  private stopRenewal(): void {
    if (this.renewTimer) {
      clearTimeout(this.renewTimer);
      this.renewTimer = undefined;
    }
  }

  /** 停机：清理续期循环（若仍是主，租约将自然过期）。 */
  shutdown(): void {
    this.stopRenewal();
    this.role = 'idle';
    this.token = undefined;
  }
}
