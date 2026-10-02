import { SeededRng } from './rng.ts';

/**
 * 离散时间（协议周期）驱动的 SWIM 风格故障检测仿真。
 *
 * 每个协议周期内，每个存活节点随机选取一个对等节点发起直接探测 (ping)；
 * 直接探测失败时，通过 k 个中间节点发起间接探测 (ping-req)。
 * 目标不可达时被标记为“怀疑 (suspected)”；被怀疑节点若在怀疑超时
 * (suspicionTimeout) 个周期内未能反驳（提高 incarnation 并 gossip），
 * 则被最终标记为“故障 (failed)”。
 */

export interface SwimOptions {
  /** 怀疑超时：怀疑持续多少个周期未反驳即确认故障。 */
  suspicionTimeout: number;
  /** 间接探测的中间节点数量 k。 */
  indirectProbes: number;
}

export interface SwimEventSink {
  /** 节点首次进入怀疑状态。 */
  onSuspect(nodeId: string, period: number): void;
  /** 被怀疑节点成功反驳（仍健康），怀疑解除。 */
  onRefute(nodeId: string, period: number): void;
  /** 怀疑超时，节点被最终标记为故障。 */
  onConfirmFailed(nodeId: string, period: number): void;
}

interface SimNode {
  readonly id: string;
  /** 地面真值：节点是否真实宕机。 */
  crashed: boolean;
  /** 地面真值：剩余丢包扰动周期数（>0 时收发全部丢失）。 */
  packetLossRemaining: number;
  /** 被怀疑的起始周期；null 表示未被怀疑。 */
  suspectedAt: number | null;
  /** 是否已被协议最终标记为故障。 */
  failed: boolean;
}

export const NULL_SINK: SwimEventSink = {
  onSuspect: () => {},
  onRefute: () => {},
  onConfirmFailed: () => {},
};

export class SwimCluster {
  private readonly nodes: SimNode[] = [];
  private readonly rng: SeededRng;
  private readonly options: SwimOptions;
  private readonly sink: SwimEventSink;

  constructor(
    nodeCount: number,
    seed: number,
    options: SwimOptions,
    sink: SwimEventSink = NULL_SINK,
  ) {
    if (nodeCount < 3) {
      throw new Error('cluster needs at least 3 nodes');
    }
    this.rng = new SeededRng(seed);
    this.options = options;
    this.sink = sink;
    for (let i = 0; i < nodeCount; i += 1) {
      this.nodes.push({
        id: `node-${i + 1}`,
        crashed: false,
        packetLossRemaining: 0,
        suspectedAt: null,
        failed: false,
      });
    }
  }

  nodeIds(): string[] {
    return this.nodes.map((n) => n.id);
  }

  /** 故障注入：节点真实宕机（地面真值）。 */
  crash(nodeId: string): void {
    this.node(nodeId).crashed = true;
  }

  /** 扰动注入：节点在 duration 个周期内丢包（短暂网络扰动，节点仍健康）。 */
  injectPacketLoss(nodeId: string, duration: number): void {
    this.node(nodeId).packetLossRemaining = duration;
  }

  isMarkedFailed(nodeId: string): boolean {
    return this.node(nodeId).failed;
  }

  /** 推进一个协议周期。 */
  runPeriod(period: number): void {
    this.resolveRefutations(period);
    this.resolveSuspicionTimeouts(period);
    this.runProbeRound(period);
    for (const node of this.nodes) {
      if (node.packetLossRemaining > 0) {
        node.packetLossRemaining -= 1;
      }
    }
  }

  /** 健康且通信恢复的节点反驳怀疑。 */
  private resolveRefutations(period: number): void {
    for (const node of this.nodes) {
      if (
        node.suspectedAt !== null &&
        !node.failed &&
        !node.crashed &&
        node.packetLossRemaining === 0
      ) {
        node.suspectedAt = null;
        this.sink.onRefute(node.id, period);
      }
    }
  }

  /** 怀疑持续超时仍未反驳 -> 最终标记故障。 */
  private resolveSuspicionTimeouts(period: number): void {
    for (const node of this.nodes) {
      if (
        node.suspectedAt !== null &&
        !node.failed &&
        period - node.suspectedAt >= this.options.suspicionTimeout
      ) {
        node.failed = true;
        this.sink.onConfirmFailed(node.id, period);
      }
    }
  }

  /** 一轮 SWIM 探测：直接 ping + 间接 ping-req。 */
  private runProbeRound(period: number): void {
    for (const prober of this.nodes) {
      if (!this.canCommunicate(prober)) {
        continue;
      }
      const target = this.pickProbeTarget(prober);
      if (target === null) {
        continue;
      }
      if (this.reachable(target) || this.indirectProbeSucceeds(prober, target)) {
        continue;
      }
      if (target.suspectedAt === null) {
        target.suspectedAt = period;
        this.sink.onSuspect(target.id, period);
      }
    }
  }

  private indirectProbeSucceeds(prober: SimNode, target: SimNode): boolean {
    const helpers = this.nodes.filter(
      (n) => n !== prober && n !== target && this.canCommunicate(n),
    );
    const shuffled = [...helpers];
    for (let i = shuffled.length - 1; i > 0; i -= 1) {
      const j = this.rng.int(i + 1);
      [shuffled[i], shuffled[j]] = [shuffled[j], shuffled[i]];
    }
    const attempts = shuffled.slice(0, this.options.indirectProbes);
    // 任一中间节点能触达目标即视为间接探测成功；
    // 中间节点均可通信，因此等价于目标本身可达且存在可用中间节点。
    return attempts.length > 0 && this.reachable(target);
  }

  private pickProbeTarget(prober: SimNode): SimNode | null {
    const candidates = this.nodes.filter((n) => n !== prober && !n.failed);
    if (candidates.length === 0) {
      return null;
    }
    return this.rng.pick(candidates);
  }

  private canCommunicate(node: SimNode): boolean {
    return !node.crashed && !node.failed && node.packetLossRemaining === 0;
  }

  private reachable(node: SimNode): boolean {
    return !node.crashed && node.packetLossRemaining === 0;
  }

  private node(nodeId: string): SimNode {
    const found = this.nodes.find((n) => n.id === nodeId);
    if (!found) {
      throw new Error(`unknown node: ${nodeId}`);
    }
    return found;
  }
}
