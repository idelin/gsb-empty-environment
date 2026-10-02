/**
 * CacheClient —— 带本地缓存的客户端。
 *
 * 持有有效读租约期间,读操作直接命中本地缓存,绝不访问服务端,
 * 且服务端保证此时文件不会被修改(零读脏)。
 * 收到服务端撤销通知时,立即失效本地缓存并确认。
 */

import { LeaseServer } from './lease-server.ts';
import type { Lease } from './lease-server.ts';

interface CacheEntry {
  readonly content: string;
  readonly lease: Lease;
}

export class CacheClient {
  public readonly id: string;
  private readonly server: LeaseServer;
  private readonly cache = new Map<string, CacheEntry>();
  private connected = true;

  /** 观测用:撤销通知次数。 */
  public revokeNotifications = 0;

  public constructor(id: string, server: LeaseServer) {
    this.id = id;
    this.server = server;
    this.server.registerRevokeCallback(id, async (lease) => {
      this.revokeNotifications += 1;
      this.cache.delete(lease.file);
      return true; // 确认已失效本地缓存
    });
  }

  /** 读取文件:租约有效期内直接返回本地缓存。 */
  public async read(file: string): Promise<string> {
    const entry = this.cache.get(file);
    if (entry !== undefined && this.server.isLeaseValid(entry.lease)) {
      return entry.content; // 租约保护期内,绝对放心
    }
    const grant = this.server.acquireReadLease(file, this.id);
    this.cache.set(file, { content: grant.content, lease: grant.lease });
    return grant.content;
  }

  /** 写文件(可能被服务端阻塞,直到他人租约失效)。 */
  public async write(file: string, content: string): Promise<void> {
    await this.server.write(file, this.id, content);
    this.cache.delete(file);
  }

  /** 模拟失联(网络分区/崩溃):撤销回调无法送达。 */
  public disconnect(): void {
    if (this.connected) {
      this.server.unregisterRevokeCallback(this.id);
      this.connected = false;
    }
  }

  /** 模拟恢复连接。 */
  public reconnect(): void {
    if (!this.connected) {
      this.connected = true;
      this.cache.clear(); // 失联期间缓存不可信,全部丢弃
      this.server.registerRevokeCallback(this.id, async (lease) => {
        this.revokeNotifications += 1;
        this.cache.delete(lease.file);
        return true;
      });
    }
  }

  public hasValidCache(file: string): boolean {
    const entry = this.cache.get(file);
    return entry !== undefined && this.server.isLeaseValid(entry.lease);
  }
}
