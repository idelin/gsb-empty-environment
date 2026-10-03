/**
 * 局部性感知路由器。
 *
 * 路由流程：key → 虚拟分片 → 候选节点集（HRW 排名的副本集）
 * → 按 (网络距离, 负载) 字典序挑选目标节点，优先同机架/同 DC，
 * 节点过载时向次近节点溢出（spillover），从而最小化跨节点通信。
 *
 * 并发安全：路由读取不可变快照；重平衡在异步互斥锁内串行执行，
 * 重算后原子替换快照，读写永不交错产生部分状态。
 */
import { LOCALITY } from "./topology.ts";
import type { Topology, NodeInfo } from "./topology.ts";
import { VirtualShardMap, shardOfKey, fnv1a } from "./vnodes.ts";

export interface RouteDecision {
  key: string;
  shardId: number;
  nodeId: string;
  /** 客户端到目标节点的网络距离 */
  distance: number;
  /** 是否因过载溢出到非首选节点 */
  spilled: boolean;
}

export interface RouterOptions {
  /** 副本候选集大小 */
  replicaCount: number;
  /** 节点负载阈值系数 */
  overloadFactor: number;
}

const DEFAULT_OPTIONS: RouterOptions = {
  replicaCount: 3,
  overloadFactor: 1.0,
};

/** 简单异步互斥锁（FIFO 顺序） */
export class AsyncMutex {
  private tail: Promise<void> = Promise.resolve();

  async runExclusive<T>(fn: () => T | Promise<T>): Promise<T> {
    const prev = this.tail;
    let release!: () => void;
    this.tail = new Promise<void>((resolve) => (release = resolve));
    await prev;
    try {
      return await fn();
    } finally {
      release();
    }
  }
}

export class LocalityAwareRouter {
  private readonly options: RouterOptions;
  private readonly mutex = new AsyncMutex();
  private loads = new Map<string, number>();
  private shardMap: VirtualShardMap;
  private readonly topology: Topology;

  constructor(
    topology: Topology,
    shardCount: number,
    options: Partial<RouterOptions> = {},
  ) {
    this.topology = topology;
    this.options = { ...DEFAULT_OPTIONS, ...options };
    this.shardMap = new VirtualShardMap(shardCount, topology);
    this.shardMap.rebalance();
  }

  get shardCount(): number {
    return this.shardMap.shardCount;
  }

  /** 某分片的候选节点（主节点 + HRW 排名副本，去重） */
  candidates(shardId: number): string[] {
    const nodes = this.topology.allNodes();
    const ranked = nodes
      .map((n) => ({ nodeId: n.nodeId, score: fnv1a(`cand:${shardId}#${n.nodeId}`) }))
      .sort((a, b) => b.score - a.score);
    const primary = this.shardMap.nodeOfShard(shardId);
    const result = [primary];
    for (const { nodeId } of ranked) {
      if (result.length >= this.options.replicaCount) break;
      if (!result.includes(nodeId)) result.push(nodeId);
    }
    return result;
  }

  /**
   * 局部性感知路由：
   * 在候选节点中按 (距离升序, 负载升序) 挑选；首选节点过载则溢出。
   */
  route(key: string, clientNodeId?: string): RouteDecision {
    const shardId = shardOfKey(key, this.shardMap.shardCount);
    const candidates = this.candidates(shardId);

    let best: { nodeId: string; distance: number; load: number } | null = null;
    for (const nodeId of candidates) {
      const distance = clientNodeId
        ? this.topology.distance(clientNodeId, nodeId)
        : LOCALITY.SAME_NODE;
      const load = this.loads.get(nodeId) ?? 0;
      const capacity = this.topology.get(nodeId).capacity;
      if (load >= capacity * this.options.overloadFactor * 100) continue;
      if (
        !best ||
        distance < best.distance ||
        (distance === best.distance && load < best.load)
      ) {
        best = { nodeId, distance, load };
      }
    }
    if (!best) {
      // 全部过载：退回主节点，保证可路由
      const primary = this.shardMap.nodeOfShard(shardId);
      best = {
        nodeId: primary,
        distance: clientNodeId
          ? this.topology.distance(clientNodeId, primary)
          : LOCALITY.SAME_NODE,
        load: this.loads.get(primary) ?? 0,
      };
    }

    const primary = this.shardMap.nodeOfShard(shardId);
    return {
      key,
      shardId,
      nodeId: best.nodeId,
      distance: best.distance,
      spilled: best.nodeId !== primary,
    };
  }

  /** 模拟请求进入：增加节点在途负载 */
  acquire(nodeId: string): void {
    if (!this.topology.has(nodeId)) throw new Error(`节点不存在: ${nodeId}`);
    this.loads.set(nodeId, (this.loads.get(nodeId) ?? 0) + 1);
  }

  /** 请求完成：释放负载；严格配对，低于 0 视为泄漏 */
  release(nodeId: string): void {
    const load = this.loads.get(nodeId) ?? 0;
    if (load <= 0) throw new Error(`负载计数下溢（泄漏）: ${nodeId}`);
    this.loads.set(nodeId, load - 1);
  }

  loadOf(nodeId: string): number {
    return this.loads.get(nodeId) ?? 0;
  }

  /** 节点变更后在锁内串行重平衡，原子替换分片映射 */
  async rebalance(): Promise<{ moved: number }> {
    return this.mutex.runExclusive(() => {
      const result = this.shardMap.rebalance();
      for (const nodeId of [...this.loads.keys()]) {
        if (!this.topology.has(nodeId)) this.loads.delete(nodeId);
      }
      this.shardMap.assertConsistent();
      return { moved: result.moved.length };
    });
  }

  /** 原子地加入节点并重平衡（避免路由线程观察到中间态） */
  async addNode(node: NodeInfo): Promise<{ moved: number }> {
    return this.mutex.runExclusive(() => {
      this.topology.addNode(node);
      const result = this.shardMap.rebalance();
      this.shardMap.assertConsistent();
      return { moved: result.moved.length };
    });
  }

  /** 原子地移除节点并重平衡；同步清理其负载计数 */
  async removeNode(nodeId: string): Promise<{ moved: number }> {
    return this.mutex.runExclusive(() => {
      this.topology.removeNode(nodeId);
      const result = this.shardMap.rebalance();
      this.loads.delete(nodeId);
      this.shardMap.assertConsistent();
      return { moved: result.moved.length };
    });
  }

  /** 无泄漏自检：分片守恒 + 无悬挂引用 + 负载计数非负 */
  assertNoLeak(): void {
    this.shardMap.assertConsistent();
    for (const [nodeId, load] of this.loads) {
      if (load < 0) throw new Error(`负载计数为负（泄漏）: ${nodeId}`);
      if (!this.topology.has(nodeId)) {
        throw new Error(`已移除节点仍持有负载（泄漏）: ${nodeId}`);
      }
    }
  }
}
