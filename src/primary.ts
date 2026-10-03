import { LeaseManager, type LeaseOptions } from "./lease.ts";
import type { Clock } from "./clock.ts";
import type { FollowerReplica } from "./follower.ts";

export class NotPrimaryError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "NotPrimaryError";
  }
}

export interface ReadResult {
  key: string;
  value: unknown;
  /** fast = 租约安全窗口内零网络本地读；degraded = 全量心跳确认后本地读 */
  path: "fast" | "degraded";
  term: number;
}

export interface OptimizerStats {
  fastPathReads: number;
  degradedReads: number;
  /** 降级路径触发的全量心跳轮次 */
  networkRounds: number;
}

/**
 * 基于租约的多副本强一致读优化器（主节点）。
 *
 * 读路径决策：
 *  - 租约处于绝对安全窗口（剩余 > safeWindowMs，默认 100ms）：
 *    零网络开销，直接本地读取最新已提交数据，保证严格线性一致。
 *  - 剩余租约不足 safeWindowMs：自动降级，向全部从节点发起一轮心跳，
 *    获得法定多数确认并续租后才本地返回，绝不返回过期数据。
 */
export class LeaseReadOptimizer {
  private readonly lease: LeaseManager;
  private readonly followers: FollowerReplica[];
  private readonly ttlMs: number;
  private readonly store = new Map<string, unknown>();
  private readonly stats: OptimizerStats = { fastPathReads: 0, degradedReads: 0, networkRounds: 0 };

  constructor(leaseOptions: LeaseOptions, followers: FollowerReplica[], clock: Clock) {
    this.lease = new LeaseManager(leaseOptions, clock);
    this.followers = followers;
    this.ttlMs = leaseOptions.ttlMs;
  }

  /** 启动/续租：全量心跳，法定多数确认后租约生效。 */
  async acquireLease(): Promise<void> {
    await this.confirmLeadership();
  }

  /** 写入只发生在主节点并同步复制（此处简化为本地提交，读侧保证线性一致）。 */
  write(key: string, value: unknown): void {
    this.store.set(key, value);
  }

  async read(key: string): Promise<ReadResult> {
    if (this.lease.isInAbsoluteSafeWindow()) {
      this.stats.fastPathReads += 1;
      return this.localRead(key, "fast");
    }
    // 剩余租约不足安全窗口：降级为全量网络心跳确认。
    await this.confirmLeadership();
    this.stats.degradedReads += 1;
    return this.localRead(key, "degraded");
  }

  private localRead(key: string, path: "fast" | "degraded"): ReadResult {
    return {
      key,
      value: this.store.get(key) ?? null,
      path,
      term: this.lease.currentTerm(),
    };
  }

  /** 向全部从节点发起一轮心跳，须获得法定多数 ack，否则主节点下台并拒绝读。 */
  private async confirmLeadership(): Promise<void> {
    this.stats.networkRounds += 1;
    const term = this.lease.currentTerm() + 1;
    const promisedUntil = Date.now() + this.ttlMs;
    const results = await Promise.allSettled(
      this.followers.map((f) => f.onHeartbeat({ term, promisedUntil })),
    );
    const acks = results.filter((r) => r.status === "fulfilled" && r.value.ok).length;
    const quorum = Math.floor(this.followers.length / 2) + 1;
    if (acks < quorum) {
      throw new NotPrimaryError(
        `lost quorum: ${acks}/${this.followers.length} acks, refuse to serve reads`,
      );
    }
    this.lease.grant();
  }

  getStats(): OptimizerStats {
    return { ...this.stats };
  }

  leaseRemainingMs(): number {
    return this.lease.remainingMs();
  }
}
