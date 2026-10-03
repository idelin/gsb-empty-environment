import { HashRing } from "./ring.ts";
import {
  DEFAULT_POLICY,
  healthFactor,
  weightFor,
  type WeightPolicyOptions,
} from "./weight-policy.ts";
import type { NodeMetrics, NodeSnapshot } from "./types.ts";

interface NodeState {
  id: string;
  metrics: NodeMetrics;
  weight: number;
}

export interface CycleReport {
  cycle: number;
  changed: boolean;
  nodes: NodeSnapshot[];
}

/**
 * Capacity-aware consistent-hash distributor.
 *
 * Nodes report real-time CPU/memory metrics; on every cycle the distributor
 * recomputes each node's health factor, rescales its virtual-node weight and
 * rebuilds the ring. Hot nodes shed virtual nodes, so their share of the key
 * space collapses and traffic spills over to neighbouring nodes on the ring.
 */
export class CapacityAwareDistributor {
  private readonly policy: WeightPolicyOptions;
  private readonly nodes = new Map<string, NodeState>();
  private readonly ring = new HashRing();
  private cycle = 0;

  constructor(policy: Partial<WeightPolicyOptions> = {}) {
    this.policy = { ...DEFAULT_POLICY, ...policy };
  }

  addNode(id: string, metrics: NodeMetrics = { cpu: 0, memory: 0 }): void {
    if (this.nodes.has(id)) throw new Error(`node already exists: ${id}`);
    this.nodes.set(id, { id, metrics, weight: weightFor(metrics, this.policy) });
    this.rebuild();
  }

  removeNode(id: string): void {
    if (!this.nodes.delete(id)) throw new Error(`unknown node: ${id}`);
    this.rebuild();
  }

  /** Report the latest real-time metrics for a node (takes effect next cycle). */
  reportMetrics(id: string, metrics: NodeMetrics): void {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`unknown node: ${id}`);
    node.metrics = metrics;
  }

  /**
   * Advance one capacity-sensing cycle: recompute weights from the latest
   * metrics and rebuild the ring if any weight changed.
   */
  runCycle(): CycleReport {
    this.cycle += 1;
    let changed = false;
    for (const node of this.nodes.values()) {
      const next = weightFor(node.metrics, this.policy);
      if (next !== node.weight) {
        node.weight = next;
        changed = true;
      }
    }
    if (changed) this.rebuild();
    return { cycle: this.cycle, changed, nodes: this.snapshot() };
  }

  /** Route a key to its owning node. */
  route(key: string): string {
    const nodeId = this.ring.lookup(key);
    if (nodeId === null) throw new Error("no nodes in the ring");
    return nodeId;
  }

  snapshot(): NodeSnapshot[] {
    return [...this.nodes.values()].map((node) => ({
      id: node.id,
      metrics: { ...node.metrics },
      effectiveLoad: Math.max(node.metrics.cpu, node.metrics.memory),
      healthFactor: healthFactor(node.metrics, this.policy),
      weight: node.weight,
    }));
  }

  private rebuild(): void {
    this.ring.rebuild(
      new Map([...this.nodes.values()].map((n) => [n.id, n.weight])),
    );
  }
}
