import { SimulatedNetwork } from "./network.ts";
import { PaxosNode, Role } from "./paxos-node.ts";
import { makeLogStore } from "./log-store.ts";
import { ObjectPool } from "./pool.ts";
import { ZeroAllocCache } from "./cache.ts";
import type { LogEntry } from "./types.ts";

/**
 * Cluster — deterministic 5-node (by default) Multi-Paxos testbed with a
 * manually advanced logical clock, partitionable network, and per-node
 * pooled log stores. Tests drive time and message delivery explicitly.
 */
export class Cluster {
  readonly net = new SimulatedNetwork();
  readonly nodes: PaxosNode[] = [];
  readonly pools: ObjectPool<LogEntry>[] = [];
  readonly caches: ZeroAllocCache<LogEntry>[] = [];
  readonly leaseMs: number;

  private clock = 0;

  constructor(size = 5, leaseMs = 1000) {
    this.leaseMs = leaseMs;
    const allIds = Array.from({ length: size }, (_, i) => i);
    for (const id of allIds) {
      const { store, pool, cache } = makeLogStore(`node${id}`);
      this.pools.push(pool);
      this.caches.push(cache);
      this.nodes.push(
        new PaxosNode({
          id,
          allIds,
          store,
          net: this.net,
          now: () => this.clock,
          leaseMs,
        })
      );
    }
  }

  now(): number {
    return this.clock;
  }

  /** Advance logical time and let every node observe expiry. */
  advance(ms: number): void {
    this.clock += ms;
    for (const n of this.nodes) n.tick();
  }

  flush(): number {
    return this.net.flush();
  }

  partition(groupA: number[], groupB: number[]): void {
    this.net.partition(groupA, groupB);
  }

  heal(): void {
    this.net.healAll();
  }

  /** Run an election for the given node and deliver all messages. */
  elect(nodeId: number): PaxosNode {
    const node = this.nodes[nodeId];
    node.becomeLeader();
    this.flush();
    if (!node.isLeader) throw new Error(`node ${nodeId} failed to win election`);
    return node;
  }

  leader(): PaxosNode | undefined {
    return this.nodes.find((n) => n.role === Role.Leader);
  }

  /** Propose on the current leader (or trigger election on the given node). */
  propose(nodeId: number, value: string): void {
    this.nodes[nodeId].propose(value);
    this.flush();
  }

  /** Assert helper: every node's applied log is identical. */
  appliedLogs(): string[][] {
    return this.nodes.map((n) => [...n.appliedValues()]);
  }

  /** Total pooled LogEntry objects currently checked out across the cluster. */
  outstandingEntries(): number {
    return this.pools.reduce((sum, p) => sum + p.outstanding, 0);
  }

  /** Return every pooled entry; afterwards outstandingEntries() === 0. */
  dispose(): void {
    for (const node of this.nodes) node.store.dispose();
  }
}
