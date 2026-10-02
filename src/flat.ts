/**
 * 扁平全网广播（基线）。
 *
 * 每个节点每个周期把心跳直接广播给全部 N-1 个对等体。
 * 这是"不分层"的对照组：要达到与分层方案相同的故障感知 SLO，
 * 广播周期必须一样短，单节点包量因此是 O(N)/周期。
 */

import type { Endpoint, Network, Simulator } from './sim.ts';
import type { FlatHeartbeatMessage } from './protocol.ts';

export interface FlatConfig {
  intervalMs: number;
  suspectTimeoutMs: number;
}

export const DEFAULT_FLAT_CONFIG: FlatConfig = {
  intervalMs: 25,
  suspectTimeoutMs: 150,
};

export interface FlatNodeOptions {
  id: number;
  nodeCount: number;
  sim: Simulator;
  net: Network;
  config: FlatConfig;
  nodes: FlatBroadcastNode[];
  rand: () => number;
}

export class FlatBroadcastNode implements Endpoint {
  readonly id: number;
  alive = true;
  crashedAt = -1;

  private sim: Simulator;
  private net: Network;
  private cfg: FlatConfig;
  private rand: () => number;
  private nodes: FlatBroadcastNode[];

  private heartbeat = 0;
  private counters: Float64Array;
  private updatedAt: Float64Array;
  private suspected: Uint8Array;
  readonly suspectAt: Float64Array;

  private data = new Map<string, number>();
  readonly dataLearnedAt = new Map<string, number>();

  constructor(opts: FlatNodeOptions) {
    this.id = opts.id;
    this.sim = opts.sim;
    this.net = opts.net;
    this.cfg = opts.config;
    this.rand = opts.rand;
    this.nodes = opts.nodes;
    const n = opts.nodeCount;
    this.counters = new Float64Array(n);
    this.updatedAt = new Float64Array(n);
    this.suspected = new Uint8Array(n);
    this.suspectAt = new Float64Array(n).fill(-1);
  }

  start(): void {
    this.sim.schedule(this.rand() * this.cfg.intervalMs, () => this.tick());
  }

  crash(): void {
    this.alive = false;
    this.crashedAt = this.sim.now;
  }

  broadcastData(key: string, version: number): void {
    this.data.set(key, version);
    if (!this.dataLearnedAt.has(key)) {
      this.dataLearnedAt.set(key, this.sim.now);
    }
  }

  dataVersion(key: string): number | undefined {
    return this.data.get(key);
  }

  private tick(): void {
    if (!this.alive) return;
    this.detectFailures();
    this.heartbeat++;
    this.counters[this.id] = this.heartbeat;
    this.updatedAt[this.id] = this.sim.now;
    const msg: FlatHeartbeatMessage = {
      from: this.id,
      counter: this.heartbeat,
      data: [...this.data],
    };
    for (const peer of this.nodes) {
      if (peer.id !== this.id) this.net.send(this, peer, msg);
    }
    this.sim.schedule(this.cfg.intervalMs, () => this.tick());
  }

  receive(msg: unknown): void {
    const hb = msg as FlatHeartbeatMessage;
    if (!Number.isFinite(hb.counter)) return;
    if (hb.counter > this.counters[hb.from]) {
      this.counters[hb.from] = hb.counter;
      this.updatedAt[hb.from] = this.sim.now;
      if (this.suspected[hb.from] === 1) {
        this.suspected[hb.from] = 0;
        this.suspectAt[hb.from] = -1;
      }
    }
    for (const [key, version] of hb.data) {
      const current = this.data.get(key);
      if (current === undefined || current < version) {
        this.data.set(key, version);
        if (!this.dataLearnedAt.has(key)) {
          this.dataLearnedAt.set(key, this.sim.now);
        }
      }
    }
  }

  private detectFailures(): void {
    const now = this.sim.now;
    for (let peer = 0; peer < this.nodes.length; peer++) {
      if (peer === this.id || this.suspected[peer] === 1) continue;
      if (now - this.updatedAt[peer] > this.cfg.suspectTimeoutMs) {
        this.suspected[peer] = 1;
        this.suspectAt[peer] = now;
      }
    }
  }
}
