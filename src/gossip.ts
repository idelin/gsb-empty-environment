import { randomUUID } from 'node:crypto';
import { performance } from 'node:perf_hooks';
import { MembershipTable } from './membership.ts';
import { TcpTransport } from './transport.ts';
import type { Transport } from './transport.ts';
import type { BootstrapReport, MemberMeta, Message, NodeAddress, NodeId } from './types.ts';

export interface GossipNodeOptions {
  id?: NodeId;
  host?: string;
  port?: number;
  seeds?: NodeAddress[];
  gossipIntervalMs?: number;
  fanout?: number;
  requestTimeoutMs?: number;
  transport?: Transport;
}

export class GossipNode {
  readonly id: NodeId;
  readonly membership: MembershipTable;
  address: NodeAddress | null = null;
  bootstrapReport: BootstrapReport | null = null;
  gossipRounds = 0;
  gossipMessagesReceived = 0;

  private readonly transport: Transport;
  private readonly seeds: NodeAddress[];
  private readonly gossipIntervalMs: number;
  private readonly fanout: number;
  private readonly requestTimeoutMs: number;
  private timer: NodeJS.Timeout | null = null;

  constructor(options: GossipNodeOptions = {}) {
    this.id = options.id ?? randomUUID();
    this.transport = options.transport ?? new TcpTransport();
    this.seeds = options.seeds ?? [];
    this.gossipIntervalMs = options.gossipIntervalMs ?? 200;
    this.fanout = options.fanout ?? 3;
    this.requestTimeoutMs = options.requestTimeoutMs ?? 800;
    this.membership = new MembershipTable(this.id);
  }

  async start(): Promise<void> {
    const address = await this.transport.listen('127.0.0.1', 0);
    this.address = address;
    this.transport.onMessage((msg, respond) => this.handleMessage(msg, respond));
    this.membership.upsert({
      id: this.id,
      address,
      heartbeat: 1,
      state: 'alive',
      lastUpdate: Date.now(),
    });
    if (this.seeds.length > 0) {
      await this.bootstrap();
    }
    this.timer = setInterval(() => {
      void this.gossipRound();
    }, this.gossipIntervalMs);
  }

  async bootstrap(): Promise<BootstrapReport> {
    const startedAt = performance.now();
    const self = this.membership.get(this.id)!;
    const replies = await Promise.all(
      this.seeds.map((seed) =>
        this.transport.request(seed, { type: 'BOOTSTRAP_REQUEST', from: self }, this.requestTimeoutMs),
      ),
    );
    let contactedSeeds = 0;
    let learnedMembers = 0;
    for (const reply of replies) {
      if (reply && reply.type === 'BOOTSTRAP_RESPONSE') {
        contactedSeeds += 1;
        learnedMembers += this.membership.merge(reply.members);
      }
    }
    this.bootstrapReport = {
      contactedSeeds,
      totalSeeds: this.seeds.length,
      learnedMembers,
      knownMembers: this.membership.size,
      durationMs: performance.now() - startedAt,
    };
    return this.bootstrapReport;
  }

  shuffleRing(): MemberMeta[] {
    const peers = this.membership.alive().filter((meta) => meta.id !== this.id);
    for (let i = peers.length - 1; i > 0; i -= 1) {
      const j = Math.floor(Math.random() * (i + 1));
      [peers[i], peers[j]] = [peers[j], peers[i]];
    }
    return peers;
  }

  async gossipRound(): Promise<number> {
    const targets = this.shuffleRing().slice(0, this.fanout);
    if (targets.length === 0) return 0;
    this.gossipRounds += 1;
    const sample = this.membership.list();
    await Promise.all(
      targets.map(async (peer) => {
        const reply = await this.transport.request(
          peer.address,
          { type: 'GOSSIP_SYNC', from: this.id, members: sample },
          this.requestTimeoutMs,
        );
        if (reply && reply.type === 'GOSSIP_SYNC') {
          this.membership.merge(reply.members);
        }
      }),
    );
    return targets.length;
  }

  private handleMessage(msg: Message, respond: (reply: Message) => void): void {
    switch (msg.type) {
      case 'BOOTSTRAP_REQUEST': {
        this.membership.merge([msg.from]);
        respond({ type: 'BOOTSTRAP_RESPONSE', members: this.membership.list() });
        break;
      }
      case 'GOSSIP_SYNC': {
        this.gossipMessagesReceived += 1;
        this.membership.merge(msg.members);
        respond({ type: 'GOSSIP_SYNC', from: this.id, members: this.membership.list() });
        break;
      }
      default:
        break;
    }
  }

  async stop(): Promise<void> {
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }
    await this.transport.close();
  }
}
