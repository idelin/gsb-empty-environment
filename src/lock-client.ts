import { EventEmitter } from 'node:events';
import {
  LeaseExpiredError,
  LeaseLockServer,
  type LeaseGrant,
} from './lease-lock-server.ts';

export interface AcquireOptions {
  /** Automatically renew the lease in a background loop. Default: true. */
  keepAlive?: boolean;
  /** Heartbeat interval. Default: ttlMs / 3. */
  keepAliveIntervalMs?: number;
  /** Max time to wait in the server queue. Default: wait forever. */
  waitTimeoutMs?: number;
}

/**
 * A held lock. While `keepAlive` is active a background timer keeps the
 * server-side lease alive; `suspend()` simulates a crashed/frozen node by
 * dropping heartbeats, so the lease expires naturally on the server.
 *
 * Emits:
 *  - 'lost'  when the lease is gone (detected via a failed heartbeat)
 */
export class LockHandle extends EventEmitter {
  readonly grant: LeaseGrant;
  private readonly server: LeaseLockServer;
  private heartbeat?: ReturnType<typeof setInterval>;
  private isSuspended = false;
  private _lost = false;

  constructor(server: LeaseLockServer, grant: LeaseGrant) {
    super();
    this.server = server;
    this.grant = grant;
  }

  get key(): string {
    return this.grant.key;
  }

  get fenceToken(): number {
    return this.grant.fenceToken;
  }

  /** True once the lease is known to be lost (heartbeat was rejected). */
  get lost(): boolean {
    return this._lost;
  }

  get suspended(): boolean {
    return this.isSuspended;
  }

  startKeepAlive(intervalMs: number): void {
    this.stopKeepAlive();
    this.heartbeat = setInterval(() => this.beat(), intervalMs);
    this.heartbeat.unref?.();
  }

  stopKeepAlive(): void {
    if (this.heartbeat) clearInterval(this.heartbeat);
    this.heartbeat = undefined;
  }

  /** Simulate a crashed / frozen node: heartbeats silently stop. */
  suspend(): void {
    this.isSuspended = true;
  }

  /**
   * Simulate the node coming back. Probes the server once: if the lease
   * already expired, the handle is marked lost and stays fenced out.
   */
  resume(): void {
    this.isSuspended = false;
    if (this._lost) return;
    try {
      this.server.renew(this.grant.leaseId);
    } catch (err) {
      if (err instanceof LeaseExpiredError) this.markLost();
      else throw err;
    }
  }

  /** Fence-protected write through this handle. */
  write(value: unknown): void {
    this.server.write(this.grant.key, this.grant.fenceToken, value);
  }

  /** Voluntarily release. Returns false if the lease was already gone. */
  release(): boolean {
    this.stopKeepAlive();
    if (this._lost) return false;
    const ok = this.server.release(this.grant.leaseId);
    if (!ok) this.markLost();
    return ok;
  }

  private beat(): void {
    if (this.isSuspended || this._lost) return;
    try {
      this.server.renew(this.grant.leaseId);
    } catch (err) {
      if (err instanceof LeaseExpiredError) this.markLost();
      else throw err;
    }
  }

  private markLost(): void {
    if (this._lost) return;
    this._lost = true;
    this.stopKeepAlive();
    this.emit('lost', this.grant);
  }
}

/** Client facade bound to one node identity. */
export class LockClient {
  private readonly server: LeaseLockServer;
  readonly nodeId: string;

  constructor(server: LeaseLockServer, nodeId: string) {
    this.server = server;
    this.nodeId = nodeId;
  }

  async acquire(key: string, ttlMs: number, opts: AcquireOptions = {}): Promise<LockHandle> {
    const { keepAlive = true, keepAliveIntervalMs, waitTimeoutMs } = opts;
    const grant = await this.server.acquire(key, this.nodeId, ttlMs, waitTimeoutMs);
    const handle = new LockHandle(this.server, grant);
    if (keepAlive) handle.startKeepAlive(keepAliveIntervalMs ?? Math.max(1, Math.floor(ttlMs / 3)));
    return handle;
  }
}
