import { LossyNetwork } from "./network.ts";
import { ClusterNode } from "./node.ts";
import type { SyncStats } from "./node.ts";
import type { StateRecord } from "./state.ts";
import { mulberry32 } from "./hash.ts";

export interface SimulationOptions {
  nodeCount?: number;
  dropRate?: number;
  seed?: number;
}

/**
 * Deterministic in-memory cluster: drives gossip rounds and anti-entropy
 * rounds synchronously so tests and the demo are fully reproducible.
 */
export class Simulation {
  readonly network: LossyNetwork;
  readonly nodes: ClusterNode[] = [];
  private rng: () => number;
  private logicalClock = 0;

  constructor(options: SimulationOptions = {}) {
    const nodeCount = options.nodeCount ?? 6;
    const dropRate = options.dropRate ?? 0.9;
    const seed = options.seed ?? 42;
    this.rng = mulberry32(seed);
    this.network = new LossyNetwork(dropRate, this.rng);
    for (let i = 0; i < nodeCount; i++) {
      this.nodes.push(new ClusterNode(`node-${i}`, this.network, this.rng));
    }
  }

  tick(): number {
    return ++this.logicalClock;
  }

  /** Give every node the same initial UP membership view. */
  bootstrapCluster(): void {
    for (const node of this.nodes) {
      for (const member of this.nodes) {
        node.bootstrap({
          nodeId: member.nodeId,
          status: "UP",
          incarnation: 1,
          updatedAt: 0,
        });
      }
    }
  }

  markDown(nodeId: string): void {
    const node = this.nodes.find((candidate) => candidate.nodeId === nodeId);
    if (!node) throw new Error(`unknown node: ${nodeId}`);
    node.markSelf("DOWN", 2, this.tick());
  }

  gossipRound(): void {
    for (const node of this.nodes) node.gossipTick();
  }

  /** One anti-entropy round; returns per-node sync stats (sparse). */
  antiEntropyRound(): (SyncStats | null)[] {
    return this.nodes.map((node) => node.antiEntropyTick());
  }

  isConverged(): boolean {
    const reference = this.nodes[0];
    return this.nodes.every((node) => node.isConvergedWith(reference));
  }

  /**
   * Keys on which `node` is missing or stale relative to the newest record
   * known anywhere in the cluster (the "ground truth" for assertions).
   */
  missingKeys(node: ClusterNode): string[] {
    const truth = new Map<string, StateRecord>();
    for (const peer of this.nodes) {
      for (const record of peer.table.all()) {
        const current = truth.get(record.nodeId);
        if (
          !current ||
          record.incarnation > current.incarnation ||
          (record.incarnation === current.incarnation && record.updatedAt > current.updatedAt)
        ) {
          truth.set(record.nodeId, record);
        }
      }
    }
    const missing: string[] = [];
    for (const [nodeId, record] of truth) {
      const local = node.table.get(nodeId);
      if (
        !local ||
        local.incarnation !== record.incarnation ||
        local.updatedAt !== record.updatedAt ||
        local.status !== record.status
      ) {
        missing.push(nodeId);
      }
    }
    return missing.sort();
  }
}
