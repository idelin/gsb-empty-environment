/**
 * Test/demo harness: a cluster of PaxosNode instances over one Network,
 * plus a retrying Client that hides leader discovery from callers.
 */
import { Network } from './network.ts';
import { PaxosNode, NotLeaderError, type Command } from './node.ts';

export interface ClusterOptions {
  size?: number;
  leaseMs?: number;
  latency?: () => number;
}

export class Cluster {
  readonly net: Network;
  readonly nodes: PaxosNode[];

  constructor(opts: ClusterOptions = {}) {
    const size = opts.size ?? 5;
    this.net = new Network(opts.latency ?? (() => 0));
    const ids = Array.from({ length: size }, (_, i) => i + 1);
    this.nodes = ids.map(
      (id) => new PaxosNode(id, ids, this.net, { leaseMs: opts.leaseMs ?? 200 }),
    );
  }

  node(id: number): PaxosNode {
    const n = this.nodes[id - 1];
    if (!n) throw new Error(`no node ${id}`);
    return n;
  }

  get ids(): number[] {
    return this.nodes.map((n) => n.id);
  }

  partition(groupA: number[], groupB: number[]): void {
    this.net.partition(groupA, groupB);
  }

  heal(): void {
    this.net.healAll();
  }

  leaders(): PaxosNode[] {
    return this.nodes.filter((n) => n.isLeader);
  }

  /** Wait for full network quiescence. */
  async settle(): Promise<void> {
    await this.net.drain();
  }
}

export interface SubmitOptions {
  /** Try this node first (e.g. the believed leader). */
  prefer?: PaxosNode;
  maxAttempts?: number;
}

export class Client {
  private seq = 0;
  readonly id: string;
  private readonly cluster: Cluster;

  constructor(id: string, cluster: Cluster) {
    this.id = id;
    this.cluster = cluster;
  }

  /**
   * Submit a command exactly once (retries are deduplicated by the leader).
   * Resolves with the committed slot once the command is linearized.
   */
  async submit(data: string, opts: SubmitOptions = {}): Promise<number> {
    const value: Command = { clientId: this.id, seq: this.seq++, data };
    const maxAttempts = opts.maxAttempts ?? 12;
    let lastError: unknown = new Error('no attempt made');
    for (let attempt = 0; attempt < maxAttempts; attempt++) {
      const candidates = opts.prefer
        ? [opts.prefer, ...this.cluster.nodes.filter((n) => n !== opts.prefer)]
        : this.cluster.nodes;
      for (const n of candidates) {
        try {
          if (!n.isLeader && !(await n.campaign())) continue;
          return await n.propose(value);
        } catch (err) {
          if (err instanceof NotLeaderError) {
            lastError = err;
            continue;
          }
          throw err;
        }
      }
    }
    throw lastError;
  }
}

/** Deterministic PRNG (mulberry32) for reproducible randomized tests. */
export function prng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
