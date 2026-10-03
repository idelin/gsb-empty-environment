/**
 * CacheClient —— 租约协议客户端。
 *
 *  - read():租约有效期内直接返回本地缓存(零服务端往返,且保证不脏);
 *           无有效租约时向服务端申请读租约并填充缓存。
 *  - 收到服务端 revoke 回调时:立即作废本地缓存并 ack(除非模拟失联)。
 *  - write():委托服务端执行,服务端会阻塞到所有读租约失效后才落盘。
 */
import { LeaseServer } from "./leaseServer.ts";
import type { LeaseGrant, WriteResult } from "./leaseServer.ts";

interface CacheEntry {
  data: string;
  version: number;
  leaseId: string;
  expiresAt: number;
}

export const DEFAULT_LEASE_TTL_MS = 5_000; // 默认 5 秒租约

export class CacheClient {
  public readonly id: string;
  private server: LeaseServer;
  private cache = new Map<string, CacheEntry>();
  /** 模拟客户端失联:收到 revoke 时不作废缓存、不 ack。 */
  public simulateUnreachable = false;
  /** 观测用:本地缓存命中次数。 */
  public localHits = 0;
  private now: () => number;

  constructor(id: string, server: LeaseServer, opts: { now?: () => number } = {}) {
    this.id = id;
    this.server = server;
    this.now = opts.now ?? (() => Date.now());
  }

  /** 读取文件:租约期内命中本地缓存,否则向服务端申请租约(默认 5 秒)。 */
  async read(file: string): Promise<string> {
    const cached = this.cache.get(file);
    if (cached && this.now() < cached.expiresAt) {
      this.localHits++;
      return cached.data; // 租约保护期内,绝对安全
    }
    this.cache.delete(file);
    return this.readWithLease(file, DEFAULT_LEASE_TTL_MS);
  }

  /** 带自定义租约时长的读。 */
  async readWithLease(file: string, ttlMs: number): Promise<string> {
    const grant: LeaseGrant = await this.server.requestReadLease(
      file,
      this.id,
      ttlMs,
      (leaseId, f) => this.onRevoke(leaseId, f),
    );
    this.cache.set(file, {
      data: grant.data,
      version: grant.version,
      leaseId: grant.leaseId,
      expiresAt: grant.expiresAt,
    });
    return grant.data;
  }

  write(file: string, data: string): Promise<WriteResult> {
    return this.server.write(file, data);
  }

  /** 服务端撤销回调:作废本地缓存并确认。 */
  private onRevoke(leaseId: string, file: string): void {
    if (this.simulateUnreachable) return; // 失联:不应答
    const cached = this.cache.get(file);
    if (cached && cached.leaseId === leaseId) {
      this.cache.delete(file); // 立即失效本地缓存
    }
    this.server.ackRevoke(leaseId, this.id);
  }

  hasValidCache(file: string): boolean {
    const cached = this.cache.get(file);
    return !!cached && this.now() < cached.expiresAt;
  }
}
