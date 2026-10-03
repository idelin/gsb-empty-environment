import { VirtualClock, type TimerHandle } from './clock.ts';
import { SimulatedNetwork, type Message } from './network.ts';

export type MemberState = 'alive' | 'suspect' | 'dead';

export interface SwimOptions {
  /** Length of one protocol period in ms (default 1000). */
  periodMs?: number;
  /** How long to wait for a direct Ack before falling back to PingReq (default 300). */
  ackTimeoutMs?: number;
  /** Number of indirect probe (PingReq) proxies, K (default 3). */
  indirectK?: number;
  /** How long a member stays `suspect` before being declared `dead` (default 2 periods). */
  suspicionTimeoutMs?: number;
  /** Random source in [0, 1); injectable for deterministic tests. */
  rng?: () => number;
}

interface PendingProbe {
  seq: number;
  target: string;
  acked: boolean;
}

interface PendingProxyRequest {
  origin: string;
  target: string;
}

/**
 * A SWIM-style failure detector.
 *
 * Each protocol period the node picks a random alive member and sends it a
 * Ping. If no Ack arrives within `ackTimeoutMs`, it asks K random proxy
 * members to ping the target on its behalf (PingReq). The probe succeeds if
 * any direct or indirect Ack arrives before the end of the period, which
 * greatly reduces false positives on lossy direct links. A target that
 * cannot be reached by the end of the period is marked `suspect`, and
 * `dead` after `suspicionTimeoutMs` without a refuting Ack.
 */
export class SwimNode {
  readonly id: string;
  readonly members = new Map<string, MemberState>();
  private readonly net: SimulatedNetwork;
  private readonly clock: VirtualClock;
  private readonly periodMs: number;
  private readonly ackTimeoutMs: number;
  private readonly indirectK: number;
  private readonly suspicionTimeoutMs: number;
  private readonly rng: () => number;
  private seq = 0;
  private probe: PendingProbe | null = null;
  private readonly proxyRequests = new Map<string, PendingProxyRequest>();
  private readonly suspicionTimers = new Map<string, TimerHandle>();

  constructor(
    id: string,
    net: SimulatedNetwork,
    clock: VirtualClock,
    options: SwimOptions = {},
  ) {
    this.id = id;
    this.net = net;
    this.clock = clock;
    this.periodMs = options.periodMs ?? 1000;
    this.ackTimeoutMs = options.ackTimeoutMs ?? 300;
    this.indirectK = options.indirectK ?? 3;
    this.suspicionTimeoutMs = options.suspicionTimeoutMs ?? 2 * this.periodMs;
    this.rng = options.rng ?? Math.random;
  }

  join(peerIds: Iterable<string>): void {
    for (const peer of peerIds) {
      if (peer !== this.id) this.members.set(peer, 'alive');
    }
  }

  /** Start the periodic probe cycle. */
  start(): void {
    this.clock.schedule(this.periodMs, () => this.tick());
  }

  stateOf(peer: string): MemberState | undefined {
    return this.members.get(peer);
  }

  receive(message: Message): void {
    switch (message.type) {
      case 'ping': {
        this.net.send({
          type: 'ack',
          from: this.id,
          to: message.from,
          seq: message.seq,
          onBehalfOf: message.onBehalfOf,
        });
        break;
      }
      case 'ack': {
        if (message.onBehalfOf && message.onBehalfOf !== this.id) {
          // We acted as a proxy for this ping: relay the Ack to the originator.
          const key = `${message.onBehalfOf}:${message.seq}`;
          const pending = this.proxyRequests.get(key);
          if (pending) {
            this.proxyRequests.delete(key);
            // The relayed Ack is sent by us (the proxy), so it travels the
            // healthy proxy->origin link even when origin<->target is down.
            this.net.send({
              type: 'ack',
              from: this.id,
              to: pending.origin,
              seq: message.seq,
              target: pending.target,
            });
          }
          break;
        }
        const probe = this.probe;
        if (
          probe &&
          !probe.acked &&
          probe.seq === message.seq &&
          (message.target ?? message.from) === probe.target
        ) {
          probe.acked = true;
          this.markAlive(probe.target);
        }
        break;
      }
      case 'ping-req': {
        const key = `${message.from}:${message.seq}`;
        this.proxyRequests.set(key, { origin: message.from, target: message.target });
        this.net.send({
          type: 'ping',
          from: this.id,
          to: message.target,
          seq: message.seq,
          onBehalfOf: message.from,
        });
        // Give up on this proxy request if the target never answers.
        this.clock.schedule(this.periodMs, () => this.proxyRequests.delete(key));
        break;
      }
    }
  }

  private tick(): void {
    this.clock.schedule(this.periodMs, () => this.tick());
    const target = this.pickRandom(this.alivePeers());
    if (target === undefined) return;
    const seq = ++this.seq;
    const probe: PendingProbe = { seq, target, acked: false };
    this.probe = probe;
    this.net.send({ type: 'ping', from: this.id, to: target, seq });
    // Direct probe phase: fall back to indirect probes on Ack timeout.
    this.clock.schedule(this.ackTimeoutMs, () => {
      if (!probe.acked) this.startIndirectProbe(probe);
    });
    // End of period: no Ack via any path => suspect the target.
    this.clock.schedule(this.periodMs, () => {
      if (!probe.acked) this.markSuspect(target);
    });
  }

  private startIndirectProbe(probe: PendingProbe): void {
    const proxies = this.pickProxies(probe.target, this.indirectK);
    for (const proxy of proxies) {
      this.net.send({
        type: 'ping-req',
        from: this.id,
        to: proxy,
        target: probe.target,
        seq: probe.seq,
      });
    }
  }

  private markAlive(peer: string): void {
    if (!this.members.has(peer)) return;
    this.members.set(peer, 'alive');
    const timer = this.suspicionTimers.get(peer);
    if (timer !== undefined) {
      this.clock.cancel(timer);
      this.suspicionTimers.delete(peer);
    }
  }

  private markSuspect(peer: string): void {
    if (this.members.get(peer) !== 'alive') return;
    this.members.set(peer, 'suspect');
    const timer = this.clock.schedule(this.suspicionTimeoutMs, () => {
      if (this.members.get(peer) === 'suspect') this.members.set(peer, 'dead');
    });
    this.suspicionTimers.set(peer, timer);
  }

  private alivePeers(): string[] {
    const peers: string[] = [];
    for (const [id, state] of this.members) {
      if (state === 'alive') peers.push(id);
    }
    return peers;
  }

  private pickRandom<T>(items: T[]): T | undefined {
    if (items.length === 0) return undefined;
    return items[Math.floor(this.rng() * items.length)];
  }

  /** Pick up to `k` distinct random alive members other than `target`. */
  private pickProxies(target: string, k: number): string[] {
    const candidates = this.alivePeers().filter((id) => id !== target);
    const chosen: string[] = [];
    while (chosen.length < k && candidates.length > 0) {
      const idx = Math.floor(this.rng() * candidates.length);
      chosen.push(candidates.splice(idx, 1)[0]);
    }
    return chosen;
  }
}
