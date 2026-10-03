/**
 * 虚拟分片（vnode）映射层。
 *
 * 将固定数量 V 个虚拟分片通过 Rendezvous（HRW）哈希分配到物理节点：
 * - 节点增删时，仅与被增删节点相关的分片发生迁移（最小迁移性）；
 * - 分片数远大于节点数，天然获得细粒度均衡；
 * - 容量权重通过按权重展开节点槽位实现。
 *
 * 不变式（无泄漏断言的基础）：
 *   每个分片恰好映射到一个存活节点；分片总数守恒；无悬挂引用。
 */
import { Topology } from "./topology.ts";
import type { NodeInfo } from "./topology.ts";

/** FNV-1a 32 位哈希 + Murmur3 finalizer（保证相似键的雪崩效应） */
export function fnv1a(input: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < input.length; i++) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  hash ^= hash >>> 16;
  hash = Math.imul(hash, 0x85ebca6b);
  hash ^= hash >>> 13;
  hash = Math.imul(hash, 0xc2b2ae35);
  hash ^= hash >>> 16;
  return hash >>> 0;
}

/** key → 虚拟分片号 */
export function shardOfKey(key: string, shardCount: number): number {
  return fnv1a(key) % shardCount;
}

/** Rendezvous 哈希打分 */
function rendezvousScore(shardId: number, nodeSlot: string): number {
  return fnv1a(`${shardId}#${nodeSlot}`);
}

export interface ShardAssignment {
  shardId: number;
  nodeId: string;
}

export interface RebalanceResult {
  assignment: ReadonlyMap<number, string>;
  /** 相对上一次映射发生迁移的分片 */
  moved: ShardAssignment[];
}

export class VirtualShardMap {
  private assignment = new Map<number, string>();
  readonly shardCount: number;
  private readonly topology: Topology;

  constructor(shardCount: number, topology: Topology) {
    if (!Number.isInteger(shardCount) || shardCount <= 0) {
      throw new Error("shardCount 必须为正整数");
    }
    this.shardCount = shardCount;
    this.topology = topology;
  }

  /**
   * 按当前拓扑重算映射（Rendezvous 哈希 + 容量权重）。
   * 返回迁移分片列表；调用方应持有并发锁。
   */
  rebalance(): RebalanceResult {
    const nodes = this.topology.allNodes();
    if (nodes.length === 0) {
      if (this.assignment.size > 0) {
        throw new Error("拓扑为空但存在未释放的分片映射（泄漏）");
      }
      return { assignment: new Map(), moved: [] };
    }

    // 容量权重 → 槽位展开：权重越大，赢得分片的概率越高
    const slots: { nodeId: string; slot: string }[] = [];
    for (const node of nodes) {
      const weight = Math.max(1, Math.round(node.capacity));
      for (let i = 0; i < weight; i++) {
        slots.push({ nodeId: node.nodeId, slot: `${node.nodeId}@${i}` });
      }
    }

    const next = new Map<number, string>();
    const moved: ShardAssignment[] = [];
    for (let shard = 0; shard < this.shardCount; shard++) {
      let bestNode = "";
      let bestScore = -1;
      for (const { nodeId, slot } of slots) {
        const score = rendezvousScore(shard, slot);
        if (score > bestScore) {
          bestScore = score;
          bestNode = nodeId;
        }
      }
      next.set(shard, bestNode);
      if (this.assignment.get(shard) !== bestNode) {
        moved.push({ shardId: shard, nodeId: bestNode });
      }
    }

    this.assertConsistent(next);
    this.assignment = next;
    return { assignment: new Map(next), moved };
  }

  /** 当前映射的只读快照 */
  snapshot(): ReadonlyMap<number, string> {
    return new Map(this.assignment);
  }

  nodeOfShard(shardId: number): string {
    const nodeId = this.assignment.get(shardId);
    if (nodeId === undefined) throw new Error(`分片未分配: ${shardId}`);
    return nodeId;
  }

  nodeOfKey(key: string): string {
    return this.nodeOfShard(shardOfKey(key, this.shardCount));
  }

  /** 每个节点持有的分片数 */
  distribution(): Map<string, number> {
    const dist = new Map<string, number>();
    for (const nodeId of this.assignment.values()) {
      dist.set(nodeId, (dist.get(nodeId) ?? 0) + 1);
    }
    return dist;
  }

  /** 无泄漏不变式：分片全覆盖、唯一归属、无悬挂节点引用 */
  assertConsistent(map: ReadonlyMap<number, string> = this.assignment): void {
    if (map.size !== this.shardCount) {
      throw new Error(
        `分片泄漏: 期望 ${this.shardCount} 个分片，实际 ${map.size}`,
      );
    }
    for (let shard = 0; shard < this.shardCount; shard++) {
      if (!map.has(shard)) throw new Error(`分片 ${shard} 未分配（泄漏）`);
    }
    for (const nodeId of map.values()) {
      if (!this.topology.has(nodeId)) {
        throw new Error(`分片映射到已移除节点 ${nodeId}（悬挂引用）`);
      }
    }
  }
}

/** 构建均匀拓扑的便捷函数 */
export function buildUniformTopology(
  dcCount: number,
  racksPerDc: number,
  nodesPerRack: number,
  capacity = 1,
): Topology {
  const topo = new Topology();
  for (let d = 0; d < dcCount; d++) {
    for (let r = 0; r < racksPerDc; r++) {
      for (let n = 0; n < nodesPerRack; n++) {
        const node: NodeInfo = {
          nodeId: `dc${d}-rack${r}-node${n}`,
          rackId: `dc${d}-rack${r}`,
          dcId: `dc${d}`,
          capacity,
        };
        topo.addNode(node);
      }
    }
  }
  return topo;
}
