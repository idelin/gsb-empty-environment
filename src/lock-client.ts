import { EventEmitter } from "node:events";
import { LeaseLostError } from "./errors.ts";
import type { LeaseSnapshot, LockServer } from "./lock-server.ts";

export interface AcquireOptions {
  /** 租约 TTL（毫秒）。到期未续期，锁自动释放。 */
  ttlMs: number;
  /** 心跳间隔（毫秒），默认 ttlMs / 3。 */
  heartbeatIntervalMs?: number;
  /** 用于取消排队中的 acquire。 */
  signal?: AbortSignal;
}

export type LockHandleState = "held" | "released" | "lost";

export interface LockHandleEvents {
  /** 每次心跳续期成功。 */
  renewed: (lease: LeaseSnapshot) => void;
  /** 租约丢失（心跳续期失败 / 恢复时发现锁已易主）。 */
  lost: (err: unknown) => void;
}

/**
 * 一把锁的客户端句柄。持有期间由后台心跳自动续期（KeepAlive）；
 * 一旦续期失败（租约过期、锁已易主），句柄进入 lost 状态并发出 'lost' 事件。
 */
export class LockHandle extends EventEmitter {
  readonly lockName: string;
  readonly ownerId: string;
  /** 获取锁时服务端颁发的 fencing token。 */
  readonly fenceToken: number;

  private readonly server: LockServer;
  private readonly ttlMs: number;
  private readonly intervalMs: number;
  private lease: LeaseSnapshot;
  private heartbeat: NodeJS.Timeout | undefined;
  private state: LockHandleState = "held";
  private suspended = false;

  constructor(
    server: LockServer,
    lease: LeaseSnapshot,
    ttlMs: number,
    intervalMs: number,
  ) {
    super();
    this.server = server;
    this.lease = lease;
    this.ttlMs = ttlMs;
    this.intervalMs = intervalMs;
    this.lockName = lease.lockName;
    this.ownerId = lease.ownerId;
    this.fenceToken = lease.fenceToken;
    this.startHeartbeat();
  }

  get isHeld(): boolean {
    return this.state === "held";
  }

  get handleState(): LockHandleState {
    return this.state;
  }

  /** 当前租约到期时间（epoch 毫秒）。 */
  get expiresAt(): number {
    return this.lease.expiresAt;
  }

  /** 模拟节点假死/网络分区：停止发送心跳（租约将在 TTL 后自然过期）。 */
  suspend(): void {
    this.suspended = true;
    this.stopHeartbeat();
  }

  /**
   * 模拟节点恢复：尝试立即续期。
   * 若假死期间租约已过期（锁可能已授予他人），句柄进入 lost 状态。
   */
  resume(): void {
    if (!this.suspended || this.state !== "held") return;
    this.suspended = false;
    try {
      this.lease = this.server.renew(this.lease.leaseId, this.ttlMs);
      this.emit("renewed", this.lease);
      this.startHeartbeat();
    } catch (err) {
      this.markLost(err);
    }
  }

  /** 写操作前调用：确认仍持有锁，否则抛出 LeaseLostError。 */
  ensureHeld(): void {
    if (this.state !== "held") {
      throw new LeaseLostError(
        `lock "${this.lockName}" is not held by ${this.ownerId} (state=${this.state})`,
      );
    }
  }

  /** 主动释放锁并停止心跳。已释放/已丢失时为幂等空操作。 */
  release(): void {
    if (this.state !== "held") return;
    this.state = "released";
    this.stopHeartbeat();
    try {
      this.server.release(this.lease.leaseId);
    } catch {
      // 服务端租约已先行过期，无需处理。
    }
  }

  override on<K extends keyof LockHandleEvents>(
    event: K,
    listener: LockHandleEvents[K],
  ): this {
    return super.on(event, listener);
  }

  private startHeartbeat(): void {
    this.heartbeat = setInterval(() => this.beat(), this.intervalMs);
  }

  private stopHeartbeat(): void {
    if (this.heartbeat) {
      clearInterval(this.heartbeat);
      this.heartbeat = undefined;
    }
  }

  private beat(): void {
    if (this.state !== "held" || this.suspended) return;
    try {
      this.lease = this.server.renew(this.lease.leaseId, this.ttlMs);
      this.emit("renewed", this.lease);
    } catch (err) {
      this.markLost(err);
    }
  }

  private markLost(err: unknown): void {
    if (this.state !== "held") return;
    this.state = "lost";
    this.stopHeartbeat();
    this.emit("lost", err);
  }
}

/** 锁客户端：代表一个参与竞争的节点。 */
export class LockClient {
  private readonly server: LockServer;
  readonly nodeId: string;

  constructor(server: LockServer, nodeId: string) {
    this.server = server;
    this.nodeId = nodeId;
  }

  /**
   * 获取锁并启动后台心跳续期。
   * 锁被占用时排队等待，直到前任持有者释放或租约过期。
   */
  async acquire(
    lockName: string,
    options: AcquireOptions,
  ): Promise<LockHandle> {
    const ttlMs = options.ttlMs;
    const intervalMs =
      options.heartbeatIntervalMs ?? Math.max(1, Math.floor(ttlMs / 3));
    const lease = await this.server.acquire(
      lockName,
      this.nodeId,
      ttlMs,
      options.signal,
    );
    return new LockHandle(this.server, lease, ttlMs, intervalMs);
  }
}
