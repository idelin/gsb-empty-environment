import type { ConsistentHashRing } from "./ring.ts";

/** 节点实时负载，取值范围 [0, 1]。 */
export interface NodeLoad {
  cpu: number;
  memory: number;
}

export interface CapacityPolicy {
  /** 健康节点的基准虚拟节点数。 */
  baseWeight: number;
  /** 软阈值：负载不超过该值时保持满权重。 */
  softThreshold: number;
  /** 健康度因子下限，防止节点被完全摘除导致雪崩转移。 */
  minFactor: number;
}

export const DEFAULT_POLICY: CapacityPolicy = {
  baseWeight: 100,
  softThreshold: 0.5,
  minFactor: 0.2,
};

/**
 * 健康度因子：负载 ≤ 软阈值时为 1；超过后随 (1 - load) 线性衰减，
 * 并钳制在 [minFactor, 1]。例如阈值 0.5 时，CPU 90% → 因子 0.2。
 */
export function healthFactor(load: number, policy: CapacityPolicy = DEFAULT_POLICY): number {
  if (load <= policy.softThreshold) return 1;
  const factor = (1 - load) / (1 - policy.softThreshold);
  return Math.min(1, Math.max(policy.minFactor, factor));
}

/**
 * 容量感知器：收集节点实时负载，每个周期把健康度换算成
 * 哈希环上的虚拟节点权重，实现高负载节点收缩、自动分流泻洪。
 */
export class CapacityController {
  private loads = new Map<string, NodeLoad>();
  private readonly ring: ConsistentHashRing;
  private readonly policy: CapacityPolicy;

  constructor(ring: ConsistentHashRing, policy: CapacityPolicy = DEFAULT_POLICY) {
    this.ring = ring;
    this.policy = policy;
  }

  /** 上报节点实时负载（由监控探针调用）。 */
  reportLoad(nodeId: string, load: NodeLoad): void {
    this.loads.set(nodeId, load);
  }

  /** 由负载计算目标权重（纯函数，便于测试与审计）。 */
  targetWeight(load: NodeLoad): number {
    const pressure = Math.max(load.cpu, load.memory);
    return Math.round(this.policy.baseWeight * healthFactor(pressure, this.policy));
  }

  /**
   * 执行一个调控周期：按最新负载重算所有节点权重并应用到哈希环。
   * 返回本周期各节点生效的权重。
   */
  reconcile(): Map<string, number> {
    const applied = new Map<string, number>();
    for (const [nodeId, load] of this.loads) {
      const weight = this.targetWeight(load);
      this.ring.setNodeWeight(nodeId, weight);
      applied.set(nodeId, weight);
    }
    return applied;
  }
}
