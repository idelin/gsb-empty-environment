import { Network } from './network';
import { defaultConfig, Node, NodeConfig } from './node';
import { Message, NodeId } from './types';
import { mulberry32 } from './util';

export interface SimulationOptions {
  nodeIds?: NodeId[];
  seed?: number;
  config?: Partial<NodeConfig>;
  logger?: (line: string) => void;
}

export interface CrossGroupSuccess {
  tick: number;
  kind: 'ping' | 'ping-req';
  from: NodeId;
  to: NodeId;
}

export class Simulation {
  readonly network = new Network();
  readonly nodes = new Map<NodeId, Node>();
  readonly nodeIds: NodeId[];
  now = 0;
  readonly stats = {
    mergeProposals: 0,
    mergeAccepts: 0,
    mergeCommits: 0,
    firstCrossGroupSuccess: null as CrossGroupSuccess | null,
  };

  private formerGroups: NodeId[][] | null = null;

  constructor(opts: SimulationOptions = {}) {
    this.nodeIds = opts.nodeIds ?? [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];
    const rng = mulberry32(opts.seed ?? 42);
    const config: NodeConfig = { ...defaultConfig, ...opts.config };
    for (const id of this.nodeIds) {
      this.network.join(id, 0);
      const logger = opts.logger;
      const node = new Node(id, this.nodeIds, this.network, rng, config,
        logger ? (nid, text) => logger(`[t=${String(this.now).padStart(4)}] node-${nid}: ${text}`) : undefined);
      this.nodes.set(id, node);
    }
  }

  partition(groups: NodeId[][]): void {
    this.formerGroups = groups;
    this.network.partition(groups);
  }

  heal(): void {
    this.network.heal();
  }

  tick(): void {
    this.network.now = this.now;
    for (const node of this.nodes.values()) node.tick(this.now);
    for (const msg of this.network.deliver()) {
      this.observe(msg);
      this.nodes.get(msg.to)?.receive(msg, this.now);
    }
    this.now++;
  }

  run(ticks: number): void {
    for (let i = 0; i < ticks; i++) this.tick();
  }

  runUntil(pred: () => boolean, maxTicks: number): boolean {
    for (let i = 0; i < maxTicks; i++) {
      if (pred()) return true;
      this.tick();
    }
    return pred();
  }

  /** All nodes share one identical, fully-alive view and no handshake is in flight. */
  converged(): boolean {
    const digests = new Set<string>();
    for (const node of this.nodes.values()) {
      if (node.state !== 'IDLE') return false;
      if (node.members.size !== this.nodeIds.length) return false;
      if (node.aliveCount() !== this.nodeIds.length) return false;
      digests.add(node.digest);
    }
    return digests.size === 1;
  }

  private groupIndexOf(id: NodeId): number {
    if (!this.formerGroups) return 0;
    return this.formerGroups.findIndex((g) => g.includes(id));
  }

  private observe(msg: Message): void {
    if (msg.type === 'merge-propose') this.stats.mergeProposals++;
    if (msg.type === 'merge-accept') this.stats.mergeAccepts++;
    if (msg.type === 'merge-commit') this.stats.mergeCommits++;
    if (!this.stats.firstCrossGroupSuccess && this.formerGroups && msg.type === 'ack') {
      if (this.groupIndexOf(msg.from) !== this.groupIndexOf(msg.to)) {
        this.stats.firstCrossGroupSuccess = {
          tick: this.now,
          kind: msg.forOrigin === msg.to ? 'ping' : 'ping-req',
          from: msg.from,
          to: msg.to,
        };
      }
    }
  }
}
