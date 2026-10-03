import { mkdir, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { SimulatedNetwork } from './network.ts';
import { PaxosNode } from './node.ts';
import { LogicalClock } from './proposer.ts';

export interface ClusterOptions {
  rootDir: string;
  leaseMs?: number;
  syncOnWrite?: boolean;
}

/**
 * A simulated in-process cluster: one SimulatedNetwork, one shared logical
 * clock, N durable nodes each with its own on-disk store.
 */
export class Cluster {
  readonly network = new SimulatedNetwork();
  readonly clock = new LogicalClock(1_000);
  readonly nodes = new Map<number, PaxosNode>();
  readonly leaseMs: number;

  readonly ids: number[];
  readonly options: ClusterOptions;

  private constructor(ids: number[], options: ClusterOptions) {
    this.ids = ids;
    this.options = options;
    this.leaseMs = options.leaseMs ?? 5_000;
  }

  static async start(ids: number[], options: ClusterOptions): Promise<Cluster> {
    const cluster = new Cluster(ids, options);
    await rm(options.rootDir, { recursive: true, force: true });
    for (const id of ids) {
      const dir = join(options.rootDir, `node-${id}`);
      await mkdir(dir, { recursive: true });
      const node = await PaxosNode.open(id, ids, dir, cluster.network, {
        leaseMs: cluster.leaseMs,
        syncOnWrite: options.syncOnWrite ?? true,
        clock: cluster.clock,
      });
      cluster.nodes.set(id, node);
    }
    return cluster;
  }

  node(id: number): PaxosNode {
    const n = this.nodes.get(id);
    if (!n) throw new Error(`no node ${id}`);
    return n;
  }

  leader(): PaxosNode | undefined {
    return [...this.nodes.values()].find((n) => n.proposer.isLeader);
  }

  partition(groupA: number[], groupB: number[]): void {
    this.network.partition(groupA, groupB);
  }

  heal(): void {
    this.network.heal();
  }

  /** Assert every node converged to the same committed prefix. */
  assertConverged(): { commitIndex: number; summary: string } {
    const summaries = new Map<number, string>();
    let commitIndex = -1;
    for (const [id, node] of this.nodes) {
      summaries.set(id, node.committedSummary());
      if (commitIndex === -1) commitIndex = node.commitIndex;
      else if (commitIndex !== node.commitIndex) {
        throw new Error(`commitIndex divergence: node ${id}=${node.commitIndex} vs ${commitIndex}`);
      }
    }
    const unique = new Set(summaries.values());
    if (unique.size !== 1) {
      throw new Error(`log divergence: ${[...summaries.entries()].map(([i, s]) => `${i}:"${s}"`).join(' | ')}`);
    }
    return { commitIndex, summary: [...unique][0] };
  }

  async close(): Promise<void> {
    for (const node of this.nodes.values()) await node.close();
    this.nodes.clear();
  }
}
