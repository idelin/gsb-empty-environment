import { randomUUID } from "node:crypto";
import {
  AcquireAbortedError,
  LeaseLostError,
  NotLockHolderError,
} from "./errors.ts";

/** 一次成功授权（grant）的租约快照，对客户端只读。 */
export interface LeaseSnapshot {
  /** 本次授权的唯一 ID，每次授权都会变化（用于识别“锁已易主”）。 */
  readonly leaseId: string;
  readonly lockName: string;
  readonly ownerId: string;
  /** 单调递增的 fencing token，用于隔离僵尸持有者。 */
  readonly fenceToken: number;
  /** 租约到期时间（epoch 毫秒），届时若未续期则自动释放。 */
  readonly expiresAt: number;
}

interface LeaseRecord {
  leaseId: string;
  lockName: string;
  ownerId: string;
  fenceToken: number;
  expiresAt: number;
  timer: NodeJS.Timeout;
}

interface Waiter {
  ownerId: string;
  ttlMs: number;
  resolve: (snapshot: LeaseSnapshot) => void;
  reject: (err: Error) => void;
  signal?: AbortSignal;
  onAbort?: () => void;
}

interface LockState {
  current: LeaseRecord | null;
  queue: Waiter[];
}

/**
 * 分布式锁服务端（进程内模拟，语义对齐 etcd/ZooKeeper 的租约锁）：
 *
 * - 获取锁即绑定一个 TTL 租约，到期未续期则自动释放，防止持有者崩溃导致死锁；
 * - 每次授权颁发单调递增的 fencing token，配合受保护资源拒绝过期持有者；
 * - 等待者按 FIFO 排队，锁释放或租约过期时依次唤醒。
 */
export class LockServer {
  private readonly locks = new Map<string, LockState>();
  private readonly leaseIndex = new Map<string, LeaseRecord>();
  private fenceCounter = 0;
  private readonly now: () => number;
  private closed = false;

  constructor(now: () => number = Date.now) {
    this.now = now;
  }

  /**
   * 获取锁。若锁空闲则立即授权；否则排队等待，直到锁被释放或前任持有者租约过期。
   * 可通过 AbortSignal 取消排队。
   */
  acquire(
    lockName: string,
    ownerId: string,
    ttlMs: number,
    signal?: AbortSignal,
  ): Promise<LeaseSnapshot> {
    this.assertOpen();
    if (ttlMs <= 0) throw new RangeError("ttlMs must be positive");
    if (signal?.aborted) {
      return Promise.reject(new AcquireAbortedError());
    }
    const state = this.stateFor(lockName);
    if (state.current === null && state.queue.length === 0) {
      return Promise.resolve(this.grant(lockName, state, ownerId, ttlMs));
    }
    return new Promise<LeaseSnapshot>((resolve, reject) => {
      const waiter: Waiter = { ownerId, ttlMs, resolve, reject, signal };
      if (signal) {
        const onAbort = () => {
          const idx = state.queue.indexOf(waiter);
          if (idx >= 0) {
            state.queue.splice(idx, 1);
            reject(new AcquireAbortedError());
          }
        };
        waiter.onAbort = onAbort;
        signal.addEventListener("abort", onAbort, { once: true });
      }
      state.queue.push(waiter);
    });
  }

  /** 续期：延长租约 TTL。租约已过期/易主时抛出 LeaseLostError。 */
  renew(leaseId: string, ttlMs: number): LeaseSnapshot {
    this.assertOpen();
    const record = this.leaseIndex.get(leaseId);
    if (!record) {
      throw new LeaseLostError(`lease ${leaseId} is no longer held`);
    }
    clearTimeout(record.timer);
    record.expiresAt = this.now() + ttlMs;
    record.timer = setTimeout(() => this.expire(record), ttlMs);
    return this.snapshot(record);
  }

  /** 主动释放锁。租约不存在（已过期）时抛出 NotLockHolderError。 */
  release(leaseId: string): void {
    this.assertOpen();
    const record = this.leaseIndex.get(leaseId);
    if (!record) {
      throw new NotLockHolderError(`lease ${leaseId} is not held`);
    }
    clearTimeout(record.timer);
    this.leaseIndex.delete(leaseId);
    const state = this.stateFor(record.lockName);
    if (state.current === record) {
      state.current = null;
    }
    this.grantNext(record.lockName, state);
  }

  /** 查询某把锁当前持有者的快照（无持有者时返回 null）。 */
  inspect(lockName: string): LeaseSnapshot | null {
    const state = this.locks.get(lockName);
    return state?.current ? this.snapshot(state.current) : null;
  }

  /** 排队中的等待者数量。 */
  waiterCount(lockName: string): number {
    return this.locks.get(lockName)?.queue.length ?? 0;
  }

  /** 关闭服务端：清理所有定时器并拒绝所有排队等待者。 */
  close(): void {
    if (this.closed) return;
    this.closed = true;
    for (const record of this.leaseIndex.values()) {
      clearTimeout(record.timer);
    }
    this.leaseIndex.clear();
    for (const state of this.locks.values()) {
      state.current = null;
      for (const waiter of state.queue.splice(0)) {
        waiter.reject(new AcquireAbortedError("lock server closed"));
      }
    }
    this.locks.clear();
  }

  private stateFor(lockName: string): LockState {
    let state = this.locks.get(lockName);
    if (!state) {
      state = { current: null, queue: [] };
      this.locks.set(lockName, state);
    }
    return state;
  }

  private grant(
    lockName: string,
    state: LockState,
    ownerId: string,
    ttlMs: number,
  ): LeaseSnapshot {
    const record: LeaseRecord = {
      leaseId: randomUUID(),
      lockName,
      ownerId,
      fenceToken: ++this.fenceCounter,
      expiresAt: this.now() + ttlMs,
      timer: undefined as unknown as NodeJS.Timeout,
    };
    record.timer = setTimeout(() => this.expire(record), ttlMs);
    state.current = record;
    this.leaseIndex.set(record.leaseId, record);
    return this.snapshot(record);
  }

  private expire(record: LeaseRecord): void {
    if (!this.leaseIndex.has(record.leaseId)) return;
    this.leaseIndex.delete(record.leaseId);
    const state = this.stateFor(record.lockName);
    if (state.current !== record) return;
    state.current = null;
    this.grantNext(record.lockName, state);
  }

  private grantNext(lockName: string, state: LockState): void {
    const waiter = state.queue.shift();
    if (!waiter) return;
    if (waiter.signal && waiter.onAbort) {
      waiter.signal.removeEventListener("abort", waiter.onAbort);
    }
    const snapshot = this.grant(lockName, state, waiter.ownerId, waiter.ttlMs);
    waiter.resolve(snapshot);
  }

  private snapshot(record: LeaseRecord): LeaseSnapshot {
    return {
      leaseId: record.leaseId,
      lockName: record.lockName,
      ownerId: record.ownerId,
      fenceToken: record.fenceToken,
      expiresAt: record.expiresAt,
    };
  }

  private assertOpen(): void {
    if (this.closed) throw new Error("lock server is closed");
  }
}
