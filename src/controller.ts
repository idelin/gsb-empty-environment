import { ConsistentHashRing } from "./ring.ts";
import type { RingNode } from "./ring.ts";

export type RingRole = "active" | "standby";

export type WarmupStatus = "idle" | "warming" | "ready";

/** 一次拓扑变更：可同时增删节点。 */
export interface TopologyChange {
  add?: RingNode[];
  remove?: string[];
}

/** 单个已登记键的迁移预测。 */
export interface KeyMigration {
  key: string;
  from: string | undefined;
  to: string | undefined;
}

/** 备环预热时预先计算的键迁移影响。 */
export interface MigrationPlan {
  /** 变更描述快照。 */
  change: TopologyChange;
  /** 全哈希空间中预计易主的比例（确定性采样估算）。 */
  estimatedMovedFraction: number;
  /** 已登记键中预计迁移的键数。 */
  movedKeys: number;
  /** 已登记键总数。 */
  trackedKeys: number;
  /** 已登记键的逐键迁移明细（from === to 的未列入）。 */
  migrations: KeyMigration[];
  /** 各节点净增/净减的已登记键数。 */
  perNodeDelta: Record<string, number>;
}

export interface WarmupReport {
  status: WarmupStatus;
  plan: MigrationPlan;
  warmupMs: number;
}

export interface RouteDecision {
  key: string;
  nodeId: string | undefined;
  /** 当前服务环的世代号，每次翻转 +1，可用于检测跨代脏读。 */
  epoch: number;
  role: RingRole;
}

export interface FailoverReport {
  epoch: number;
  /** 指针翻转耗时（毫秒，亚毫秒级）。 */
  flipMs: number;
  nodeCount: number;
}

export interface ControllerOptions {
  vnodesPerWeight?: number;
  /** 估算迁移比例时的采样点数，默认 4096。 */
  estimateSamples?: number;
}

/**
 * 双环热备路由控制器。
 *
 * - Active 环承载全量流量（route() 只读 active 引用）；
 * - Standby 环在后台吸收拓扑变更，并在预热阶段预先计算键迁移影响；
 * - 主环异常时 failover() 通过一次同步引用交换完成原子指针翻转，
 *   单线程事件循环内无任何中间态，流量瞬时平滑过渡。
 */
export class DualRingController {
  private active: ConsistentHashRing;
  private standby: ConsistentHashRing;
  private epoch = 0;
  private status: WarmupStatus = "idle";
  private plan: MigrationPlan | null = null;
  private readonly trackedKeys = new Set<string>();
  private readonly estimateSamples: number;
  private readonly vnodesPerWeight: number | undefined;
  private lastFailover: FailoverReport | null = null;

  constructor(nodes: RingNode[] = [], options: ControllerOptions = {}) {
    this.vnodesPerWeight = options.vnodesPerWeight;
    this.estimateSamples = options.estimateSamples ?? 4096;
    this.active = new ConsistentHashRing(nodes, {
      vnodesPerWeight: options.vnodesPerWeight,
    });
    this.standby = this.active;
  }

  /** 登记需要精确追踪迁移影响的键（如热点键、采样键）。 */
  trackKeys(keys: Iterable<string>): void {
    for (const key of keys) this.trackedKeys.add(key);
  }

  get trackedKeyCount(): number {
    return this.trackedKeys.size;
  }

  get warmupStatus(): WarmupStatus {
    return this.status;
  }

  get currentEpoch(): number {
    return this.epoch;
  }

  /** 全量流量入口：始终读当前 active 环。 */
  route(key: string): RouteDecision {
    const ring = this.active;
    return {
      key,
      nodeId: ring.getNodeForKey(key),
      epoch: this.epoch,
      role: "active",
    };
  }

  /**
   * 向备环注入拓扑变更并同步完成预热：
   * 备环基于当前主环派生新拓扑，同时预先计算键迁移影响。
   * 全程不触碰 active 引用，主环流量零感知。
   */
  injectTopologyChange(change: TopologyChange): WarmupReport {
    this.status = "warming";
    const started = performance.now();

    let next = this.active;
    for (const nodeId of change.remove ?? []) {
      if (next.hasNode(nodeId)) next = next.removeNode(nodeId);
    }
    for (const node of change.add ?? []) {
      if (!next.hasNode(node.id)) next = next.addNode(node);
    }

    const plan = this.computePlan(this.active, next, change);
    this.standby = next;
    this.plan = plan;
    this.status = "ready";

    return { status: this.status, plan, warmupMs: performance.now() - started };
  }

  /** 预先计算键迁移影响：空间占比估算 + 已登记键的逐键精确 diff。 */
  private computePlan(
    before: ConsistentHashRing,
    after: ConsistentHashRing,
    change: TopologyChange,
  ): MigrationPlan {
    const estimatedMovedFraction = before.estimateMovedFraction(
      after,
      this.estimateSamples,
    );

    const migrations: KeyMigration[] = [];
    const perNodeDelta: Record<string, number> = {};
    for (const key of this.trackedKeys) {
      const from = before.getNodeForKey(key);
      const to = after.getNodeForKey(key);
      if (from !== to) {
        migrations.push({ key, from, to });
        if (from !== undefined) {
          perNodeDelta[from] = (perNodeDelta[from] ?? 0) - 1;
        }
        if (to !== undefined) {
          perNodeDelta[to] = (perNodeDelta[to] ?? 0) + 1;
        }
      }
    }

    return {
      change,
      estimatedMovedFraction,
      movedKeys: migrations.length,
      trackedKeys: this.trackedKeys.size,
      migrations,
      perNodeDelta,
    };
  }

  /** 最近一次预热的迁移预案（未预热时为 null）。 */
  getMigrationPlan(): MigrationPlan | null {
    return this.plan;
  }

  /**
   * 原子指针翻转：一次同步引用交换即完成全局切换。
   * 翻转期间 route() 要么读到旧环、要么读到新环，不存在混合态。
   */
  failover(): FailoverReport {
    if (this.status !== "ready") {
      throw new Error(
        `standby ring is not ready (status=${this.status}), cannot failover`,
      );
    }
    const started = performance.now();
    const previous = this.active;
    this.active = this.standby;
    this.standby = previous;
    this.epoch += 1;
    const flipMs = performance.now() - started;

    this.status = "idle";
    this.plan = null;
    const report: FailoverReport = {
      epoch: this.epoch,
      flipMs,
      nodeCount: this.active.size,
    };
    this.lastFailover = report;
    return report;
  }

  get lastFailoverReport(): FailoverReport | null {
    return this.lastFailover;
  }

  /**
   * 校验当前主环上全部已登记键的路由是否与给定预案一致（零错乱验证）。
   * 迁移键必须落在预案预测的 to 节点；未迁移键必须与旧环（翻转后的备环）一致。
   */
  verifyAgainstPlan(plan: MigrationPlan): { checked: number; mismatches: KeyMigration[] } {
    const moved = new Map<string, string | undefined>();
    for (const m of plan.migrations) moved.set(m.key, m.to);

    const mismatches: KeyMigration[] = [];
    for (const key of this.trackedKeys) {
      const actual = this.active.getNodeForKey(key);
      const want = moved.has(key)
        ? moved.get(key)
        : this.standby.getNodeForKey(key);
      if (actual !== want) {
        mismatches.push({ key, from: want, to: actual });
      }
    }
    return { checked: this.trackedKeys.size, mismatches };
  }
}
