import type { HandoverCompletedEvent, HandoverPackage, LeaseContext } from './types.ts';
import { LeaseError } from './types.ts';
import type { LeaseCoordinator } from './coordinator.ts';

export interface LeaseNodeOptions {
  /** 续期触发点：剩余 TTL 的该比例耗尽时发起续期，默认 0.5 */
  renewalFraction?: number;
  /** 续期失败时的最大重试次数，默认 3 */
  maxRenewalRetries?: number;
  /** 可注入时钟，默认 Date.now */
  now?: () => number;
}

/**
 * 租约节点：业务进程侧的租约代理。
 * 负责主角色维持（动态续期）、写冻结、平滑移交与无缝接管。
 */
export class LeaseNode {
  readonly id: string;
  private coordinator: LeaseCoordinator;
  private context: LeaseContext | null = null;
  private ttlMs = 0;
  private writesFrozen = false;
  private renewalTimer: ReturnType<typeof setTimeout> | null = null;
  private renewalFraction: number;
  private maxRenewalRetries: number;
  private now: () => number;
  private onHandoverCompleted: (event: HandoverCompletedEvent) => void;

  constructor(id: string, coordinator: LeaseCoordinator, options: LeaseNodeOptions = {}) {
    this.id = id;
    this.coordinator = coordinator;
    this.renewalFraction = options.renewalFraction ?? 0.5;
    this.maxRenewalRetries = options.maxRenewalRetries ?? 3;
    this.now = options.now ?? Date.now;
    this.onHandoverCompleted = (event) => {
      if (this.context && event.leaseId === this.context.leaseId && event.fromNodeId === this.id) {
        // 本节点已完成移交：停止续期、让出主角色（节点随即进入维护流程）
        this.stopRenewalLoop();
        this.context = null;
      }
    };
    this.coordinator.on('handover-completed', this.onHandoverCompleted);
  }

  get isPrimary(): boolean {
    return this.context !== null;
  }

  get isWritesFrozen(): boolean {
    return this.writesFrozen;
  }

  /** 当前租约上下文快照 */
  get lease(): LeaseContext | null {
    return this.context ? { ...this.context } : null;
  }

  /** 获取租约并成为主节点，同时启动动态续期 */
  acquire(resourceId: string, ttlMs: number): LeaseContext {
    this.context = this.coordinator.acquire(resourceId, this.id, ttlMs);
    this.ttlMs = ttlMs;
    this.startRenewalLoop();
    return { ...this.context };
  }

  /**
   * 滚动维护入口：冻结本地写入，向协调中心申请移交，
   * 返回交接包（由运维通道递交给候选节点）。
   */
  initiateHandover(candidateId: string): HandoverPackage {
    if (!this.context) {
      throw new LeaseError('not-primary', `node "${this.id}" holds no lease`);
    }
    this.freezeWrites();
    try {
      return this.coordinator.initiateHandover(this.context.leaseId, this.id, candidateId);
    } catch (err) {
      this.unfreezeWrites();
      throw err;
    }
  }

  /**
   * 候选人接收交接包：立即继承剩余租约上下文并接管主角色，
   * 不等待旧租约自然超时。
   */
  receiveHandover(pkg: HandoverPackage): LeaseContext {
    const context = this.coordinator.completeHandover(pkg);
    this.context = context;
    this.ttlMs = Math.max(1, context.expiresAt - this.now());
    this.startRenewalLoop();
    return { ...context };
  }

  /** 移交失败时中止并恢复本地服务 */
  abortHandover(): void {
    if (!this.context) return;
    this.coordinator.abortHandover(this.context.leaseId, this.id);
    this.unfreezeWrites();
  }

  /** 冻结本地写入（移交前置步骤） */
  freezeWrites(): void {
    this.writesFrozen = true;
  }

  /** 解冻本地写入 */
  unfreezeWrites(): void {
    this.writesFrozen = false;
  }

  /** 业务写入口：仅活跃主节点且未冻结时可写，携带 fencing token 校验 */
  write(resourceId: string, value: string): void {
    if (!this.context) {
      throw new LeaseError('not-primary', `node "${this.id}" is not primary`);
    }
    if (this.writesFrozen) {
      throw new LeaseError('writes-frozen', `node "${this.id}" has frozen writes for handover`);
    }
    this.coordinator.assertFencing(resourceId, this.context.epoch);
    void value;
  }

  /** 释放租约并卸任 */
  release(): void {
    if (!this.context) return;
    this.coordinator.release(this.context.leaseId, this.id);
    this.stopRenewalLoop();
    this.context = null;
  }

  /** 节点关闭时清理定时器与监听 */
  dispose(): void {
    this.stopRenewalLoop();
    this.coordinator.off('handover-completed', this.onHandoverCompleted);
  }

  /**
   * 动态续期循环：按剩余 TTL 的比例动态计算下一次续期时机，
   * 失败时按指数退避重试，耗尽重试次数后放弃主角色。
   */
  private startRenewalLoop(attempt = 0): void {
    this.stopRenewalLoop();
    const context = this.context;
    if (!context) return;
    const remaining = context.expiresAt - this.now();
    if (remaining <= 0) {
      this.context = null;
      return;
    }
    const delay =
      attempt === 0
        ? Math.max(1, Math.floor(remaining * this.renewalFraction))
        : Math.max(1, Math.floor((remaining * this.renewalFraction) / 2 ** attempt));
    this.renewalTimer = setTimeout(() => {
      const current = this.context;
      if (!current) return;
      try {
        this.context = this.coordinator.renew(current.leaseId, this.id, this.ttlMs);
        this.startRenewalLoop(0);
      } catch (err) {
        if ((err as LeaseError).code === 'handover-pending') return; // 移交中，静默等待
        if (attempt + 1 >= this.maxRenewalRetries) {
          this.context = null; // 续期彻底失败，放弃主角色
          return;
        }
        this.startRenewalLoop(attempt + 1);
      }
    }, delay);
    this.renewalTimer.unref?.();
  }

  private stopRenewalLoop(): void {
    if (this.renewalTimer) {
      clearTimeout(this.renewalTimer);
      this.renewalTimer = null;
    }
  }
}
