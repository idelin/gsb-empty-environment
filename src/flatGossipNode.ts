import { Network } from './network.ts';
import { Simulator } from './simulator.ts';
import { ALIVE } from './types.ts';
import type { DigestEntry, MemberRecord, Message } from './types.ts';

/**
 * Baseline: classic flat full-network gossip. Every node pushes a digest to
 * `fanout` random peers every `periodMs`. To approach sub-second global
 * failure detection at 1000-node scale the period must be aggressive
 * (~100ms), which is exactly the noise this component eliminates.
 */
export class FlatGossipNode {
  readonly id: number;
  crashed = false;

  private records = new Map<number, MemberRecord>();
  private recentChanges: DigestEntry[] = [];
  private net: Network;
  private sim: Simulator;
  private rng: () => number;
  private nodeCount: number;
  private periodMs: number;
  private fanout: number;

  constructor(
    id: number,
    nodeCount: number,
    net: Network,
    sim: Simulator,
    rng: () => number,
    periodMs = 100,
    fanout = 3,
  ) {
    this.id = id;
    this.nodeCount = nodeCount;
    this.net = net;
    this.sim = sim;
    this.rng = rng;
    this.periodMs = periodMs;
    this.fanout = fanout;
    net.register(id, (msg) => this.handleMessage(msg as Message));
  }

  start(): void {
    const tick = (): void => {
      if (this.crashed) return;
      this.gossip();
      this.sim.schedule(this.periodMs, tick);
    };
    this.sim.schedule(this.rng() * this.periodMs, tick);
  }

  crash(): void {
    this.crashed = true;
    this.net.unregister(this.id);
  }

  private handleMessage(msg: Message): void {
    if (this.crashed || msg.kind !== 'flat') return;
    for (const entry of msg.entries) {
      if (entry.id === this.id) continue;
      const current = this.records.get(entry.id);
      if (!current || entry.version > current.version) {
        this.records.set(entry.id, {
          incarnation: entry.incarnation,
          state: entry.state,
          version: entry.version,
        });
        this.recentChanges.push(entry);
        if (this.recentChanges.length > 64) this.recentChanges.splice(0, 32);
      }
    }
  }

  private gossip(): void {
    const entries: DigestEntry[] = [
      { id: this.id, incarnation: 1, state: ALIVE, version: 0 },
      ...this.recentChanges.slice(-32),
    ];
    for (let f = 0; f < this.fanout; f++) {
      const target = Math.floor(this.rng() * this.nodeCount);
      this.net.send(this.id, target, { kind: 'flat', from: this.id, entries });
    }
  }
}
