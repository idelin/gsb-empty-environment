import type { Clock } from "./clock.ts";
import { LeaseManager } from "./lease.ts";
import type { Transport } from "./transport.ts";

export type ReadSource = "lease-local" | "quorum-confirmed";

export interface ReadResult {
  readonly value: unknown;
  readonly commitIndex: number;
  readonly source: ReadSource;
  /** 本次读实际产生的网络调用次数。 */
  readonly networkCalls: number;
}

export interface LeaseReadOptimizerOptions {
  nodeId: string;
  followerIds: string[];
  clock: Clock;
  transport: Transport;
  /** 每次心跳请求/续期的租约时长（毫秒）。 */
  leaseDurationMs: number;
  /** 各节点时钟最大偏移上界 ε（毫秒）。 */
  maxClockSkewMs: number;
  /** 剩余租约低于该值时降级为全量心跳确认，默认 100ms。 */
  safeReadThresholdMs?: number;
  /** 心跳确认超时（毫秒）。 */
  heartbeatTimeoutMs?: number;
}

export class LeaseNotConfirmedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "LeaseNotConfirmedError";
  }
}

/**
 * 基于租约的多副本强一致读优化器（主节点）。
 *
 * 读路径：
 *  1. 快速路径 —— 本地租约处于绝对安全窗口：零网络开销，直接本地读取，
 *     单节点即可支撑极高吞吐；
 *  2. 降级路径 —— 剩余租约不足 100ms：向全部从节点发起一轮心跳，
 *     获得多数派确认并续期租约后才本地读取，保证线性一致性不被破坏。
 */
export class LeaseReadOptimizer {
  readonly nodeId: string;
  private readonly followerIds: string[];
  private readonly transport: Transport;
  private readonly leaseManager: LeaseManager;
  private readonly leaseDurationMs: number;
  private readonly heartbeatTimeoutMs: number;
  private readonly quorumSize: number;
  private term = 1;

  private readonly store = new Map<string, unknown>();
  private commitIndex = 0;

  private localReadCount = 0;
  private degradedReadCount = 0;

  constructor(options: LeaseReadOptimizerOptions) {
    this.nodeId = options.nodeId;
    this.followerIds = [...options.followerIds];
    this.transport = options.transport;
    this.leaseDurationMs = options.leaseDurationMs;
    this.heartbeatTimeoutMs = options.heartbeatTimeoutMs ?? 50;
    this.leaseManager = new LeaseManager({
      clock: options.clock,
      maxClockSkewMs: options.maxClockSkewMs,
      safeReadThresholdMs: options.safeReadThresholdMs,
    });
    this.quorumSize = Math.floor((this.followerIds.length + 1) / 2) + 1;
  }

  get lease(): LeaseManager {
    return this.leaseManager;
  }

  get stats(): { localReads: number; degradedReads: number } {
    return { localReads: this.localReadCount, degradedReads: this.degradedReadCount };
  }

  /** 写入：简化模型 —— 复制到多数派后在主节点提交（此处直接提交本地状态机）。 */
  write(key: string, value: unknown): number {
    this.store.set(key, value);
    return ++this.commitIndex;
  }

  /**
   * 强一致读。
   * 安全窗口内零网络开销；否则自动降级为全量心跳确认后再读。
   */
  async read(key: string): Promise<ReadResult> {
    if (this.leaseManager.isInAbsoluteSafeWindow()) {
      this.localReadCount++;
      return {
        value: this.store.get(key),
        commitIndex: this.commitIndex,
        source: "lease-local",
        networkCalls: 0,
      };
    }
    this.degradedReadCount++;
    const callsBefore = this.transport.networkCallCount;
    await this.confirmLeaseViaHeartbeat();
    return {
      value: this.store.get(key),
      commitIndex: this.commitIndex,
      source: "quorum-confirmed",
      networkCalls: this.transport.networkCallCount - callsBefore,
    };
  }

  /**
   * 降级路径：向全部从节点并行发送心跳，等待多数派确认后续期租约。
   * 未获多数派确认则抛错 —— 绝不提供可能违反线性一致性的读。
   */
  async confirmLeaseViaHeartbeat(): Promise<void> {
    const request = {
      term: this.term,
      leaderId: this.nodeId,
      requestedLeaseMs: this.leaseDurationMs,
    };
    const acks = await this.collectQuorum(
      this.followerIds.map((id) => this.transport.sendHeartbeat(id, request)),
      this.quorumSize - 1, // 主节点自身算一票
    );
    if (acks < this.quorumSize - 1) {
      throw new LeaseNotConfirmedError(
        `lease not confirmed: ${acks + 1}/${this.quorumSize} votes (quorum unreachable)`,
      );
    }
    this.leaseManager.grant(this.leaseDurationMs);
  }

  /** 收集多数派确认；在超时内未达多数派即返回已确认数量。 */
  private collectQuorum(pending: Promise<unknown>[], needed: number): Promise<number> {
    return new Promise((resolve) => {
      let acks = 0;
      let settled = 0;
      const timer = setTimeout(() => resolve(acks), this.heartbeatTimeoutMs);
      for (const p of pending) {
        p.then(
          () => {
            acks++;
            settled++;
            if (acks >= needed || settled === pending.length) {
              clearTimeout(timer);
              resolve(acks);
            }
          },
          () => {
            settled++;
            if (settled === pending.length) {
              clearTimeout(timer);
              resolve(acks);
            }
          },
        );
      }
      if (pending.length === 0) {
        clearTimeout(timer);
        resolve(0);
      }
    });
  }
}
