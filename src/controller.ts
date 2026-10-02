import type { Clock } from './clock.ts';
import type { NetworkHub } from './network.ts';
import type { FencedStorage } from './storage.ts';
import type { LeaseGrant, Witness } from './witness.ts';
import {
  LeaseHeldError,
  NetworkUnreachableError,
  NotWritableError,
  StaleLeaseCredentialError,
} from './errors.ts';

export type Role = 'primary' | 'standby';

export const WITNESS_ID = 'witness';

/**
 * 机房侧全局租约控制器。
 *
 * 安全不变式：
 *  1. 只有持有法官颁发的未过期租约时才可写（isWritable）。
 *  2. 与法官失联期间无法续租，租约在本地到期后自动停止写入 ——
 *     因此在法官向备机房颁发新租约之前，主机房必然已经停写，杜绝双写。
 *  3. 续租时被法官告知凭证失效（epoch 已被取代）→ 立即降级为只读备机。
 *  4. 所有写携带 epoch 作为 Fencing Token，存储层拒绝过期 epoch 的僵尸写。
 */
export class LeaseController {
  readonly dcId: string;
  private readonly clock: Clock;
  private readonly network: NetworkHub;
  private readonly witness: Witness;
  private readonly storage: FencedStorage;
  private readonly ttlMs: number;

  private role: Role = 'standby';
  private lease: LeaseGrant | null = null;
  private readonly eventLog: string[] = [];

  constructor(opts: {
    dcId: string;
    clock: Clock;
    network: NetworkHub;
    witness: Witness;
    storage: FencedStorage;
    ttlMs: number;
  }) {
    this.dcId = opts.dcId;
    this.clock = opts.clock;
    this.network = opts.network;
    this.witness = opts.witness;
    this.storage = opts.storage;
    this.ttlMs = opts.ttlMs;
  }

  getRole(): Role {
    return this.role;
  }

  currentLease(): LeaseGrant | null {
    return this.lease ? { ...this.lease } : null;
  }

  events(): readonly string[] {
    return this.eventLog;
  }

  private emit(event: string): void {
    this.eventLog.push(`[t=${this.clock.now()}] ${this.dcId}: ${event}`);
  }

  /** 向法官申请租约；成功则晋升为可写主节点。 */
  tryAcquire(): boolean {
    try {
      const grant = this.network.send(this.dcId, WITNESS_ID, () =>
        this.witness.requestLease(this.dcId, this.ttlMs),
      );
      this.lease = grant;
      this.role = 'primary';
      this.emit(`ACQUIRED lease epoch=${grant.epoch} expiresAt=${grant.expiresAt} -> primary`);
      return true;
    } catch (err) {
      if (err instanceof LeaseHeldError) {
        this.emit(`acquire denied: lease held by ${err.holderId} (epoch=${err.epoch})`);
        return false;
      }
      if (err instanceof NetworkUnreachableError) {
        this.emit404(err);
        return false;
      }
      throw err;
    }
  }

  private emit404(err: NetworkUnreachableError): void {
    this.emit(`witness unreachable (${err.message})`);
  }

  /**
   * 周期性的租约维护（生产环境由定时器驱动，演练中按 tick 手动调用）。
   * - 续租成功：延长本地可写窗口。
   * - 凭证失效：法官已把租约颁给别人 → 降级为只读备机。
   * - 网络失败：保持现状，但本地租约到期后 isWritable() 自动为 false。
   */
  maintain(): void {
    if (this.role !== 'primary' || !this.lease) {
      return;
    }
    const epoch = this.lease.epoch;
    try {
      this.lease = this.network.send(this.dcId, WITNESS_ID, () =>
        this.witness.renewLease(this.dcId, epoch, this.ttlMs),
      );
      this.emit(`renewed lease epoch=${epoch} expiresAt=${this.lease.expiresAt}`);
    } catch (err) {
      if (err instanceof StaleLeaseCredentialError) {
        this.demote('witness credential revoked (lease taken over by peer)');
      } else if (err instanceof NetworkUnreachableError) {
        this.emit(`renew failed: witness unreachable; local lease expires at ${this.lease.expiresAt}`);
      } else {
        throw err;
      }
    }
  }

  /** 是否可写：必须是主节点且本地持有的租约尚未过期。 */
  isWritable(): boolean {
    return (
      this.role === 'primary' &&
      this.lease !== null &&
      this.lease.expiresAt > this.clock.now()
    );
  }

  /** 全局互斥写：仅可写窗口内放行，并携带 Fencing Token 落盘。 */
  write(key: string, value: unknown): void {
    if (!this.isWritable()) {
      const reason =
        this.role !== 'primary'
          ? `role=${this.role} (read-only standby)`
          : 'lease expired locally (cannot confirm global exclusivity)';
      this.emit(`write REJECTED key=${key}: ${reason}`);
      throw new NotWritableError(this.dcId, reason);
    }
    this.storage.write(key, value, this.lease!.epoch);
    this.emit(`write ok key=${key} epoch=${this.lease!.epoch}`);
  }

  read(key: string): unknown {
    return this.storage.read(key);
  }

  private demote(reason: string): void {
    this.role = 'standby';
    this.lease = null;
    this.emit(`DEMOTED -> read-only standby (${reason})`);
  }
}
