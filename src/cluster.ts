import { Network } from './network.ts';
import { PaxosNode, type Role } from './paxos.ts';
import type { LogEntry } from './types.ts';

/** Deterministic 5-node (or n-node) cluster with a manual clock. */
export class Cluster {
  now = 0;
  readonly network = new Network();
  readonly nodes: PaxosNode[] = [];
  readonly leaseMs: number;
  readonly electionTimeoutMs: number;

  constructor(size = 5, leaseMs = 100, electionTimeoutMs = 150) {
    this.leaseMs = leaseMs;
    this.electionTimeoutMs = electionTimeoutMs;
    for (let id = 1; id <= size; id++) {
      const peers: number[] = [];
      for (let other = 1; other <= size; other++) if (other !== id) peers.push(other);
      const node = new PaxosNode({
        id,
        peers,
        network: this.network,
        leaseMs,
        // Deterministic, per-node timeout: lower ids campaign first.
        electionTimeoutMs: electionTimeoutMs + id * 10,
        now: () => this.now,
      });
      this.nodes.push(node);
      this.network.register(id, (msg) => node.handle(msg));
    }
  }

  node(id: number): PaxosNode {
    const node = this.nodes[id - 1];
    if (!node) throw new Error(`no node ${id}`);
    return node;
  }

  tick(ms = 1): void {
    this.now += ms;
    for (const node of this.nodes) node.checkTimeout();
  }

  /** Advance time until cond holds; returns false on timeout. */
  runUntil(cond: () => boolean, maxTicks = 5000): boolean {
    while (!cond() && maxTicks-- > 0) this.tick(1);
    return cond();
  }

  partition(groupA: number[], groupB: number[]): void {
    this.network.partition(groupA, groupB);
  }

  heal(): void {
    this.network.healAll();
  }

  leader(): PaxosNode | null {
    return this.nodes.find((n) => n.role === 'leader') ?? null;
  }

  /** Elect the first leader that times out. */
  electLeader(): PaxosNode {
    const ok = this.runUntil(() => this.leader() !== null);
    if (!ok) throw new Error('no leader elected');
    return this.leader() as PaxosNode;
  }

  appliedValues(id: number): LogEntry[] {
    return this.node(id).applied;
  }
}

/** Resolves once the node's commitIndex reaches `slot`; rejects if it loses leadership. */
export function awaitCommit(node: PaxosNode, slot: number): Promise<void> {
  if (node.commitIndex >= slot) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const offCommit = node.onCommit((index) => {
      if (index >= slot) {
        offCommit();
        offRole();
        resolve();
      }
    });
    const offRole = node.onRoleChange((role: Role) => {
      if (role !== 'leader') {
        offCommit();
        offRole();
        reject(new Error(`node ${node.id}: lost leadership before slot ${slot} committed`));
      }
    });
  });
}
