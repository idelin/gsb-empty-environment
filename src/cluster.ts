import { PaxosNode } from './node.ts';
import type { ProposeResult } from './node.ts';
import { Network } from './network.ts';
import type { NodeId, Value } from './types.ts';

export interface ClusterOptions {
  readonly electionTimeoutBase?: number;
  readonly electionTimeoutStep?: number;
  readonly leaseDuration?: number;
  readonly heartbeatInterval?: number;
  readonly trace?: (line: string) => void;
}

/**
 * Single-node deterministic clock driver: one virtual clock advances the
 * whole cluster tick by tick. Each tick: deliver due messages, fire node
 * timeouts, flush outboxes. Node processing order is fixed (sorted ids).
 */
export class Cluster {
  readonly nodes: PaxosNode[] = [];
  readonly network = new Network();
  now = 0;
  private readonly byId = new Map<NodeId, PaxosNode>();

  constructor(ids: readonly NodeId[], opts: ClusterOptions = {}) {
    const base = opts.electionTimeoutBase ?? 10;
    const step = opts.electionTimeoutStep ?? 7;
    const lease = opts.leaseDuration ?? 30;
    const heartbeat = opts.heartbeatInterval ?? 5;
    ids.forEach((id, i) => {
      const node = new PaxosNode({
        id,
        peers: ids,
        electionTimeout: base + i * step,
        leaseDuration: lease,
        heartbeatInterval: heartbeat,
      });
      if (opts.trace !== undefined) {
        const trace = opts.trace;
        node.trace = (line) => trace(`t=${this.now} [${id}] ${line}`);
      }
      this.nodes.push(node);
      this.byId.set(id, node);
    });
    this.nodes.sort((a, b) => (a.id < b.id ? -1 : 1));
  }

  node(id: NodeId): PaxosNode {
    const n = this.byId.get(id);
    if (n === undefined) throw new Error(`unknown node ${id}`);
    return n;
  }

  leader(): PaxosNode | undefined {
    return this.nodes.find((n) => n.isLeader);
  }

  leaders(): PaxosNode[] {
    return this.nodes.filter((n) => n.isLeader);
  }

  tick(): void {
    this.now += 1;
    for (const msg of this.network.deliverDue(this.now)) {
      this.byId.get(msg.to)!.receive(msg);
    }
    for (const node of this.nodes) node.tick(this.now);
    for (const node of this.nodes) {
      for (const msg of node.drainOutbox()) this.network.send(this.now, msg);
    }
  }

  run(ticks: number): void {
    for (let i = 0; i < ticks; i++) this.tick();
  }

  /** Ticks until `cond` holds (checked before each tick). Returns success. */
  runUntil(cond: () => boolean, maxTicks: number): boolean {
    for (let i = 0; i < maxTicks; i++) {
      if (cond()) return true;
      this.tick();
    }
    return cond();
  }

  write(id: NodeId, value: Value): ProposeResult {
    return this.node(id).propose(value);
  }

  partition(groupA: readonly NodeId[], groupB: readonly NodeId[]): void {
    for (const a of groupA) for (const b of groupB) this.network.cut(a, b);
  }

  healAll(): void {
    this.network.healAll();
  }
}
