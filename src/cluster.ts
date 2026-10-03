import { Network } from './network.ts';
import { PaxosNode } from './node.ts';
import type { ClusterEvent, LogEntry, NodeId, Role } from './types.ts';

export interface SnapshotEntry extends LogEntry {
  committed: boolean;
}

export interface SnapshotNode {
  id: NodeId;
  role: Role;
  promised: { term: number; nodeId: NodeId } | null;
  knownLeader: NodeId | null;
  leaseExpiry: number;
  commitIndex: number;
  log: SnapshotEntry[];
}

export interface ClusterSnapshot {
  time: number;
  majority: number;
  nodeIds: NodeId[];
  nodes: SnapshotNode[];
}

export interface ClusterOptions {
  leaseDuration?: number;
}

/**
 * Owns the nodes, the deterministic network and the logical clock.
 * All concurrency is explicit: messages move only when the harness
 * delivers them, time moves only via tick().
 */
export class Cluster {
  readonly nodeIds: NodeId[];
  readonly nodes = new Map<NodeId, PaxosNode>();
  readonly net: Network;
  readonly majority: number;
  time = 0;
  private listeners = new Set<(e: ClusterEvent) => void>();
  private disposed = false;

  constructor(size: number, opts: ClusterOptions = {}) {
    this.nodeIds = Array.from({ length: size }, (_, i) =>
      String.fromCharCode(65 + i),
    );
    this.majority = Math.floor(size / 2) + 1;
    this.net = new Network((to, msg) => {
      const node = this.nodes.get(to);
      if (node) node.receive(msg);
    });
    const leaseDuration = opts.leaseDuration ?? 100;
    for (const id of this.nodeIds) {
      this.nodes.set(
        id,
        new PaxosNode(
          id,
          this.net,
          { leaseDuration, majority: this.majority, peers: this.nodeIds },
          (e) => this.emit(e),
        ),
      );
    }
  }

  node(id: NodeId): PaxosNode {
    const n = this.nodes.get(id);
    if (!n) throw new Error(`unknown node ${id}`);
    return n;
  }

  onEvent(fn: (e: ClusterEvent) => void): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  private emit(e: ClusterEvent): void {
    for (const fn of this.listeners) fn(e);
  }

  tick(dt: number): void {
    this.time += dt;
    for (const n of this.nodes.values()) n.onTick(this.time);
  }

  elect(id: NodeId): void {
    this.node(id).startElection();
  }

  retryElect(id: NodeId): void {
    this.node(id).retryElection();
  }

  propose(id: NodeId, value: string): boolean {
    return this.node(id).propose(value);
  }

  partition(groups: NodeId[][]): void {
    this.net.partition(groups);
  }

  heal(): void {
    this.net.heal();
  }

  runUntilIdle(): void {
    this.net.runUntilIdle();
  }

  leader(): PaxosNode | null {
    for (const n of this.nodes.values()) {
      if (n.role === 'leader') return n;
    }
    return null;
  }

  snapshot(): ClusterSnapshot {
    return {
      time: this.time,
      majority: this.majority,
      nodeIds: [...this.nodeIds],
      nodes: this.nodeIds.map((id) => {
        const n = this.node(id);
        return {
          id,
          role: n.role,
          promised: n.promised ? { ...n.promised } : null,
          knownLeader: n.knownLeader,
          leaseExpiry: n.leaseExpiry,
          commitIndex: n.commitIndex,
          log: [...n.log.values()].map((r) => ({
            ...r.entry,
            ballot: { ...r.entry.ballot },
            committed: r.committed,
          })),
        };
      }),
    };
  }

  /**
   * Strict no-leak assertion: no undelivered messages, no dangling
   * election/accept rounds, no queued client values, no listeners
   * left after dispose. Returns a list of leaks (empty = clean).
   */
  findLeaks(): string[] {
    const leaks: string[] = [];
    if (this.net.pending > 0) {
      leaks.push(`network: ${this.net.pending} undelivered message(s)`);
    }
    for (const id of this.nodeIds) {
      const n = this.node(id);
      if (n.pendingClientValues > 0) {
        leaks.push(`${id}: ${n.pendingClientValues} queued client value(s)`);
      }
      if (n.openAcceptRounds > 0) {
        leaks.push(`${id}: ${n.openAcceptRounds} unclosed accept round(s)`);
      }
      if (n.pendingPromises > 0 && n.role !== 'leader') {
        leaks.push(`${id}: ${n.pendingPromises} dangling promise(s)`);
      }
      if (n.uncommittedChosen > 0) {
        leaks.push(`${id}: ${n.uncommittedChosen} chosen-but-uncommitted slot(s)`);
      }
    }
    if (this.disposed && this.listeners.size > 0) {
      leaks.push(`cluster: ${this.listeners.size} listener(s) after dispose`);
    }
    return leaks;
  }

  dispose(): void {
    for (const fn of this.listeners) this.listeners.delete(fn);
    this.listeners.clear();
    this.net.dispose();
    this.disposed = true;
  }
}
