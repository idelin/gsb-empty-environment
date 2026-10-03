/**
 * LeaseServer —— 基于租约(Lease)的分布式缓存失效一致性保证器(服务端)。
 *
 * 协议要点(AFS / Leases 风格):
 *  - 读客户端先向服务端申请某文件的读租约,租约期内可绝对放心地读本地缓存;
 *  - 写客户端发起写入时,若文件上存在未过期租约,服务端先向所有持租者发送
 *    撤销(revoke)回调;
 *  - 持租者确认(ack)后租约立即作废;若持租者失联,服务端安全阻塞至租约
 *    自然过期,才放行写入 —— 保证零读脏(zero dirty reads);
 *  - 写入阻塞期间,新的读租约申请会被挂起,直到写入落盘后再按新数据发放,
 *    避免"写过半途中又发旧数据租约"造成的脏读窗口。
 */

export interface LeaseGrant {
  leaseId: string;
  file: string;
  data: string;
  version: number;
  /** 租约到期时刻(epoch ms,服务端时钟)。 */
  expiresAt: number;
  ttlMs: number;
}

export interface WriteResult {
  file: string;
  version: number;
  /** 为等待租约撤销/过期而阻塞的毫秒数。 */
  waitedMs: number;
  /** 被撤销的租约数量。 */
  revokedLeases: number;
}

export type RevokeCallback = (leaseId: string, file: string) => void;

interface Lease {
  id: string;
  file: string;
  clientId: string;
  expiresAt: number;
  onRevoke: RevokeCallback;
  revoked: boolean;
}

interface FileEntry {
  data: string;
  version: number;
}

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

export class LeaseServer {
  private files = new Map<string, FileEntry>();
  /** file -> (leaseId -> Lease) */
  private leases = new Map<string, Map<string, Lease>>();
  /** file -> 串行化写入的 promise 链(写互斥,也让读方能等待在途写)。 */
  private writeChains = new Map<string, Promise<unknown>>();
  /** file -> 在途写入数(>0 时新租约申请需等待)。 */
  private pendingWrites = new Map<string, number>();
  private nextLeaseSeq = 1;
  /** 统计:服务端实际发放的租约数(用于验证客户端缓存命中)。 */
  public leasesGranted = 0;
  /** 统计:发出的撤销回调次数。 */
  public revokeNotifications = 0;
  private now: () => number;

  constructor(opts: { now?: () => number } = {}) {
    this.now = opts.now ?? (() => Date.now());
  }

  /**
   * 申请读租约;若文件不存在则自动以空内容创建。
   * 若该文件有在途写入,先挂起至写入落盘,再按最新数据发放租约。
   */
  async requestReadLease(
    file: string,
    clientId: string,
    ttlMs: number,
    onRevoke: RevokeCallback,
  ): Promise<LeaseGrant> {
    if ((this.pendingWrites.get(file) ?? 0) > 0) {
      await this.writeChains.get(file); // 等待在途写入完成
    }

    const entry = this.files.get(file) ?? { data: "", version: 0 };
    this.files.set(file, entry);
    this.leasesGranted++;

    const lease: Lease = {
      id: `lease-${this.nextLeaseSeq++}`,
      file,
      clientId,
      expiresAt: this.now() + ttlMs,
      onRevoke,
      revoked: false,
    };
    let fileLeases = this.leases.get(file);
    if (!fileLeases) {
      fileLeases = new Map();
      this.leases.set(file, fileLeases);
    }
    fileLeases.set(lease.id, lease);

    return {
      leaseId: lease.id,
      file,
      data: entry.data,
      version: entry.version,
      expiresAt: lease.expiresAt,
      ttlMs,
    };
  }

  /** 持租客户端确认撤销:租约立即作废。 */
  ackRevoke(leaseId: string, clientId: string): boolean {
    for (const fileLeases of this.leases.values()) {
      const lease = fileLeases.get(leaseId);
      if (lease && lease.clientId === clientId && !lease.revoked) {
        lease.revoked = true;
        fileLeases.delete(leaseId);
        return true;
      }
    }
    return false;
  }

  /** 查询文件当前活跃(未撤销且未过期)的租约数。 */
  activeLeaseCount(file: string): number {
    this.gcLeases(file);
    return this.leases.get(file)?.size ?? 0;
  }

  readVersion(file: string): number {
    return this.files.get(file)?.version ?? 0;
  }

  /**
   * 写入文件。若存在活跃读租约:
   *  1. 立即向所有持租者发送撤销回调;
   *  2. 等待全部 ack;对失联者,安全阻塞至其租约自然过期;
   *  3. 所有租约失效后才真正落盘 —— 零读脏。
   * 同一文件的并发写入按到达顺序串行执行。
   */
  write(file: string, data: string): Promise<WriteResult> {
    this.pendingWrites.set(file, (this.pendingWrites.get(file) ?? 0) + 1);
    const prev = this.writeChains.get(file) ?? Promise.resolve();
    const next = prev.then(() => this.doWrite(file, data));
    this.writeChains.set(file, next.catch(() => {}));
    return next;
  }

  private async doWrite(file: string, data: string): Promise<WriteResult> {
    const start = this.now();
    try {
      this.gcLeases(file);
      const fileLeases = this.leases.get(file);
      let revokedLeases = 0;

      if (fileLeases && fileLeases.size > 0) {
        revokedLeases = fileLeases.size;
        // 1) 通知所有持租者撤销(回调内应使本地缓存失效并 ack)。
        for (const lease of [...fileLeases.values()]) {
          this.revokeNotifications++;
          lease.onRevoke(lease.id, lease.file);
        }
        // 2) 等待:每个租约要么被 ack,要么阻塞到其自然过期。
        const waits = [...fileLeases.values()].map((lease) =>
          this.waitLeaseGone(file, lease),
        );
        await Promise.all(waits);
      }

      // 3) 此刻已无任何活跃租约,安全落盘。
      const entry = this.files.get(file) ?? { data: "", version: 0 };
      entry.data = data;
      entry.version += 1;
      this.files.set(file, entry);

      return {
        file,
        version: entry.version,
        waitedMs: this.now() - start,
        revokedLeases,
      };
    } finally {
      const left = (this.pendingWrites.get(file) ?? 1) - 1;
      if (left <= 0) this.pendingWrites.delete(file);
      else this.pendingWrites.set(file, left);
    }
  }

  /** 等待单个租约失效:ack 提前返回,否则阻塞到过期时刻。 */
  private async waitLeaseGone(file: string, lease: Lease): Promise<void> {
    for (;;) {
      const fileLeases = this.leases.get(file);
      if (!fileLeases || !fileLeases.has(lease.id)) return; // 已被 ack
      const remaining = lease.expiresAt - this.now();
      if (remaining <= 0) {
        fileLeases.delete(lease.id); // 自然过期,安全失效
        return;
      }
      await sleep(Math.min(remaining, 10));
    }
  }

  private gcLeases(file: string): void {
    const fileLeases = this.leases.get(file);
    if (!fileLeases) return;
    const now = this.now();
    for (const [id, lease] of fileLeases) {
      if (lease.revoked || lease.expiresAt <= now) fileLeases.delete(id);
    }
  }
}
