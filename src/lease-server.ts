/**
 * LeaseServer —— 基于租约(Lease)的分布式缓存失效一致性保证器(服务端)。
 *
 * 协议(类似 AFS / Leases):
 *  - 客户端读取文件时,服务端授予一个有时限的读租约;
 *  - 租约期内,持有者可绝对放心地读本地缓存(服务端保证文件不变);
 *  - 有写者要修改文件时,服务端先向所有租约持有者发起撤销(callback);
 *    - 持有者在线:收到撤销后使本地缓存失效并确认,写者提前放行;
 *    - 持有者失联:服务端安全阻塞,等待租约自然过期后才放行写入。
 *  由此保证"零读脏":任何客户端永远不会读到与其有效租约矛盾的数据。
 */

export interface Lease {
  readonly id: string;
  readonly file: string;
  readonly holderId: string;
  readonly grantedAt: number;
  readonly expiresAt: number;
  revoked: boolean;
}

export interface ReadGrant {
  readonly content: string;
  readonly lease: Lease;
}

/** 客户端注册的撤销回调;返回 true 表示客户端已确认失效其缓存。 */
export type RevokeCallback = (lease: Lease) => Promise<boolean>;

export interface LeaseServerOptions {
  /** 租约时长(毫秒)。 */
  readonly leaseDurationMs: number;
  /** 时钟,默认真实时间;测试可注入假时钟。 */
  readonly now?: () => number;
  /** 睡眠函数,默认真实定时器;测试可注入。 */
  readonly sleep?: (ms: number) => Promise<void>;
}

const defaultSleep = (ms: number): Promise<void> =>
  new Promise((resolve) => setTimeout(resolve, Math.max(0, ms)));

export class LeaseServer {
  private readonly leaseDurationMs: number;
  private readonly now: () => number;
  private readonly sleep: (ms: number) => Promise<void>;

  private readonly files = new Map<string, string>();
  private readonly leases = new Map<string, Lease>();
  private readonly revokeCallbacks = new Map<string, RevokeCallback>();
  private readonly writeQueues = new Map<string, Promise<void>>();
  private leaseSeq = 0;

  /** 观测用:服务端实际读取底层存储的次数。 */
  public storageReads = 0;

  public constructor(options: LeaseServerOptions) {
    if (options.leaseDurationMs <= 0) {
      throw new Error('leaseDurationMs must be positive');
    }
    this.leaseDurationMs = options.leaseDurationMs;
    this.now = options.now ?? Date.now;
    this.sleep = options.sleep ?? defaultSleep;
  }

  public currentTime(): number {
    return this.now();
  }

  /** 客户端上线时注册撤销回调(AFS callback)。 */
  public registerRevokeCallback(clientId: string, cb: RevokeCallback): void {
    this.revokeCallbacks.set(clientId, cb);
  }

  /** 客户端失联/下线(模拟网络分区、进程崩溃)。 */
  public unregisterRevokeCallback(clientId: string): void {
    this.revokeCallbacks.delete(clientId);
  }

  /** 客户端请求读租约;返回文件内容与租约。 */
  public acquireReadLease(file: string, holderId: string): ReadGrant {
    this.storageReads += 1;
    const content = this.files.get(file) ?? '';
    const grantedAt = this.now();
    const lease: Lease = {
      id: `lease-${++this.leaseSeq}`,
      file,
      holderId,
      grantedAt,
      expiresAt: grantedAt + this.leaseDurationMs,
      revoked: false,
    };
    this.leases.set(lease.id, lease);
    return { content, lease };
  }

  /** 客户端主动续租前/读缓存前可查询租约是否仍有效。 */
  public isLeaseValid(lease: Lease): boolean {
    return !lease.revoked && this.now() < lease.expiresAt;
  }

  /**
   * 写入文件。若存在其他客户端持有的有效读租约:
   *  1. 先并发通知所有持有者撤销;
   *  2. 未确认的租约,安全阻塞至其过期时刻;
   *  3. 全部租约失效后才落盘 —— 保证零读脏。
   * 同一文件的写操作串行化。
   */
  public write(file: string, writerId: string, content: string): Promise<void> {
    const prev = this.writeQueues.get(file) ?? Promise.resolve();
    const next = prev.then(() => this.writeSerialized(file, writerId, content));
    this.writeQueues.set(file, next);
    return next;
  }

  private async writeSerialized(
    file: string,
    writerId: string,
    content: string,
  ): Promise<void> {
    const blockers = [...this.leases.values()].filter(
      (lease) =>
        lease.file === file &&
        lease.holderId !== writerId &&
        this.isLeaseValid(lease),
    );

    await Promise.all(blockers.map((lease) => this.revokeOrAwait(lease)));

    // 防御性校验:落盘前不得存在任何仍有效的他人租约。
    for (const lease of this.leases.values()) {
      if (
        lease.file === file &&
        lease.holderId !== writerId &&
        this.isLeaseValid(lease)
      ) {
        throw new Error(
          `consistency violation: lease ${lease.id} still valid at write time`,
        );
      }
    }
    this.files.set(file, content);
  }

  /** 通知持有者撤销;若持有者失联,则安全阻塞至租约过期。 */
  private async revokeOrAwait(lease: Lease): Promise<void> {
    const cb = this.revokeCallbacks.get(lease.holderId);
    if (cb !== undefined) {
      const remaining = lease.expiresAt - this.now();
      // 撤销通知与租约到期赛跑:即使回调挂起,也不会超过租约期限。
      const acked = await Promise.race([
        cb(lease).catch(() => false),
        this.sleep(remaining).then(() => false as const),
      ]);
      if (acked) {
        lease.revoked = true;
        return;
      }
    }
    // 持有者失联或拒绝确认:阻塞至租约自然过期,绝不提前放行。
    const waitMs = lease.expiresAt - this.now();
    if (waitMs > 0) {
      await this.sleep(waitMs);
    }
    lease.revoked = true;
  }

  public read(file: string): string {
    return this.files.get(file) ?? '';
  }

  /** 测试/演示辅助:直接初始化文件内容(不触发租约逻辑)。 */
  public seed(file: string, content: string): void {
    this.files.set(file, content);
  }
}
