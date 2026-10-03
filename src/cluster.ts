import { ManualClock } from "./clock.ts";
import { Network } from "./network.ts";
import { Node } from "./node.ts";
import type { NodeId } from "./types.ts";

export interface ClusterConfig {
  size: number;
  leaseTtlMs?: number;
  electionTimeoutMs?: number;
  /** Stagger added per node index so elections resolve deterministically. */
  electionTimeoutStepMs?: number;
  /** Simulation step used by run(). */
  tickMs?: number;
}

/**
 * Deterministic harness wiring nodes to a shared clock and network.
 * `run()` advances simulated time; leaders renew and followers time out
 * inside `onTick`, so no real timers are involved.
 */
export class Cluster {
  readonly clock = new ManualClock();
  readonly network = new Network();
  readonly nodes: Node[] = [];
  readonly quorum: number;
  private readonly tickMs: number;

  constructor(config: ClusterConfig) {
    const leaseTtlMs = config.leaseTtlMs ?? 1000;
    const electionTimeoutMs = config.electionTimeoutMs ?? 1500;
    const stepMs = config.electionTimeoutStepMs ?? 100;
    this.tickMs = config.tickMs ?? 50;
    this.quorum = Math.floor(config.size / 2) + 1;

    for (let i = 0; i < config.size; i += 1) {
      const node = new Node(this.clock, this.network, {
        id: `node-${i}`,
        clusterSize: config.size,
        leaseTtlMs,
        electionTimeoutMs: electionTimeoutMs + i * stepMs,
        renewBeforeMs: leaseTtlMs / 2,
      });
      this.nodes.push(node);
      this.network.register(node.id, node);
    }
  }

  node(id: NodeId): Node {
    const node = this.nodes.find((n) => n.id === id);
    if (!node) throw new Error(`unknown node ${id}`);
    return node;
  }

  /** The unique serving leader, if one exists right now. */
  leader(): Node | undefined {
    return this.nodes.find((n) => n.isServingLeader());
  }

  /** Nodes that currently believe they hold a valid lease (must be <= 1). */
  servingLeaders(): Node[] {
    return this.nodes.filter((n) => n.isServingLeader());
  }

  /** Symmetric partition; pass groups of node ids, e.g. [["node-0","node-1"], ["node-2","node-3","node-4"]]. */
  partition(groups: NodeId[][]): void {
    this.network.partition(groups);
  }

  heal(): void {
    this.network.heal();
  }

  /** Advance simulated time by one tick and let every node react. */
  tick(): void {
    this.clock.advance(this.tickMs);
    for (const node of this.nodes) node.onTick();
  }

  /** Advance simulated time by `ms`. */
  run(ms: number): void {
    const target = this.clock.now() + ms;
    while (this.clock.now() < target) this.tick();
  }

  /** Elect a leader immediately (deterministic alternative to timeouts). */
  elect(id: NodeId): Node {
    const node = this.node(id);
    node.startElection();
    if (!node.isServingLeader()) throw new Error(`election of ${id} failed`);
    return node;
  }
}
