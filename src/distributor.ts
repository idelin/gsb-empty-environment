import { CapacityController, DEFAULT_POLICY, type CapacityPolicy, type NodeLoad } from "./controller.ts";
import { ConsistentHashRing } from "./ring.ts";

/**
 * 动态容量感知一致性哈希分发器：
 * 哈希环负责路由，容量感知器按周期根据实时 CPU/内存负载伸缩虚拟节点。
 */
export class LoadAwareDistributor {
  readonly ring = new ConsistentHashRing();
  readonly controller: CapacityController;

  constructor(policy: CapacityPolicy = DEFAULT_POLICY) {
    this.controller = new CapacityController(this.ring, policy);
  }

  /** 接入节点并上报初始负载。 */
  addNode(nodeId: string, load: NodeLoad): void {
    this.controller.reportLoad(nodeId, load);
    this.controller.reconcile();
  }

  /** 更新节点实时负载（下一个调控周期生效）。 */
  updateLoad(nodeId: string, load: NodeLoad): void {
    this.controller.reportLoad(nodeId, load);
  }

  /** 推进一个调控周期，返回各节点生效权重。 */
  cycle(): Map<string, number> {
    return this.controller.reconcile();
  }

  /** 路由一个键到物理节点。 */
  route(key: string): string {
    const node = this.ring.getNode(key);
    if (node === undefined) throw new Error("no available node in the ring");
    return node;
  }

  /** 统计一批键在各节点上的分布。 */
  distribute(keys: Iterable<string>): Map<string, number> {
    const counts = new Map<string, number>();
    for (const key of keys) {
      const node = this.route(key);
      counts.set(node, (counts.get(node) ?? 0) + 1);
    }
    return counts;
  }
}
