import type { Rng } from '../rng.ts';

export type MemberStatus = 'alive' | 'suspect' | 'dead';

export interface MemberUpdate {
  id: string;
  incarnation: number;
  status: MemberStatus;
}

export type SwimMessage =
  | { kind: 'ping'; seq: number; from: string; gossip: MemberUpdate[] }
  | { kind: 'ack'; seq: number; from: string; gossip: MemberUpdate[] }
  | { kind: 'ping-req'; seq: number; from: string; target: string; gossip: MemberUpdate[] };

export interface SwimConfig {
  /** Ticks between probe rounds. */
  period: number;
  /** Probe phase offset (aligns all nodes for reproducible races). */
  phase: number;
  /** Ticks until a direct ping falls back to indirect probes. */
  pingTimeout: number;
  /** Ticks until an indirect probe is declared failed. */
  pingReqTimeout: number;
  /** Ticks a member may stay suspect before being declared dead. */
  suspectTimeout: number;
  /** Number of ping-req helpers. */
  indirectCount: number;
  /** Ticks between rejoin probes sent to dead members. */
  rejoinInterval: number;
  /** How many times each update is piggybacked. */
  gossipRetransmit: number;
  /** Max updates piggybacked per message. */
  gossipMax: number;
}

export const DEFAULT_SWIM_CONFIG: SwimConfig = {
  period: 10,
  phase: 0,
  pingTimeout: 8,
  pingReqTimeout: 8,
  suspectTimeout: 48,
  indirectCount: 2,
  rejoinInterval: 100,
  gossipRetransmit: 3,
  gossipMax: 6,
};

interface MemberState {
  incarnation: number;
  status: MemberStatus;
  suspectSince: number;
}

interface PendingProbe {
  target: string;
  sentAt: number;
  /** Tick when ping-req helpers were engaged, -1 while direct-only. */
  indirectAt: number;
  /** Set when this probe is relayed on behalf of another node. */
  origin: string | null;
  originSeq: number;
}

export interface NodeContext {
  readonly now: number;
  readonly rng: Rng;
  send(to: string, msg: SwimMessage): void;
  log(entry: string): void;
}

/** Status precedence within one incarnation: dead > suspect > alive. */
const STATUS_RANK: Record<MemberStatus, number> = { alive: 0, suspect: 1, dead: 2 };

/**
 * SWIM failure detector + membership state machine.
 *
 * Arbitration rules (the code under test):
 *  - An update wins iff it has a higher incarnation, or the same incarnation
 *    with a strictly higher status rank (alive < suspect < dead).
 *  - A node never accepts its own suspect/dead declaration with an
 *    incarnation >= its own: it refutes by bumping its incarnation and
 *    broadcasting `alive`. This deterministically resolves split-brain races
 *    where two nodes declare each other dead at the same tick.
 */
export class SwimNode {
  readonly id: string;
  ownIncarnation = 1;
  stopped = false;

  private config: SwimConfig;
  private members = new Map<string, MemberState>();
  private pending = new Map<number, PendingProbe>();
  private nextSeq = 1;
  private gossip: { update: MemberUpdate; remaining: number }[] = [];

  constructor(id: string, peers: readonly string[], config: SwimConfig = DEFAULT_SWIM_CONFIG) {
    this.id = id;
    this.config = config;
    for (const peer of peers) {
      this.members.set(peer, { incarnation: 1, status: 'alive', suspectSince: -1 });
    }
  }

  /** Simulate a crash: the node stops sending and replying. */
  stop(): void {
    this.stopped = true;
  }

  /** Immutable snapshot of the local membership view (for assertions). */
  view(): Record<string, { incarnation: number; status: MemberStatus }> {
    const out: Record<string, { incarnation: number; status: MemberStatus }> = {};
    for (const [id, m] of this.members) {
      out[id] = { incarnation: m.incarnation, status: m.status };
    }
    return out;
  }

  tick(ctx: NodeContext): void {
    if (this.stopped) return;
    const now = ctx.now;

    // 1. Probe timeouts (direct -> indirect -> failed).
    for (const [seq, probe] of [...this.pending]) {
      if (probe.origin !== null) {
        // Relayed probe: give up silently; the origin times out on its own.
        if (now - probe.sentAt >= this.config.pingReqTimeout) this.pending.delete(seq);
        continue;
      }
      if (probe.indirectAt < 0 && now - probe.sentAt >= this.config.pingTimeout) {
        const helpers = this.probeTargets().filter((id) => id !== probe.target);
        const chosen: string[] = [];
        const pool = [...helpers];
        while (chosen.length < this.config.indirectCount && pool.length > 0) {
          chosen.push(pool.splice(ctx.rng.int(pool.length), 1)[0]);
        }
        for (const helper of chosen) {
          ctx.send(helper, {
            kind: 'ping-req',
            seq,
            from: this.id,
            target: probe.target,
            gossip: this.collectGossip(),
          });
        }
        probe.indirectAt = now;
        if (chosen.length === 0) this.failProbe(seq, probe, ctx);
      } else if (probe.indirectAt >= 0 && now - probe.indirectAt >= this.config.pingReqTimeout) {
        this.failProbe(seq, probe, ctx);
      }
    }

    // 2. Suspicion timeouts: suspect -> dead.
    for (const [id, member] of this.members) {
      if (member.status === 'suspect' && now - member.suspectSince >= this.config.suspectTimeout) {
        member.status = 'dead';
        member.suspectSince = -1;
        ctx.log(`t=${now} ${this.id} declare-dead ${id} inc=${member.incarnation}`);
        this.addGossip({ id, incarnation: member.incarnation, status: 'dead' });
      }
    }

    // 3. Protocol period: probe one random non-dead member.
    if ((now - this.config.phase) % this.config.period === 0) {
      const targets = this.probeTargets();
      if (targets.length > 0) this.startProbe(targets[ctx.rng.int(targets.length)], ctx);
    }

    // 4. Deterministic rejoin: ping dead members so refutations can flow.
    if (this.config.rejoinInterval > 0 && now % this.config.rejoinInterval === 0) {
      for (const [id, member] of this.members) {
        if (member.status === 'dead') this.rejoinProbe(id, ctx);
      }
    }

    // 5. Compact spent gossip.
    this.gossip = this.gossip.filter((entry) => entry.remaining > 0);
  }

  onMessage(ctx: NodeContext, msg: SwimMessage): void {
    if (this.stopped) return;
    this.applyGossip(msg.gossip, ctx);
    switch (msg.kind) {
      case 'ping':
        ctx.send(msg.from, { kind: 'ack', seq: msg.seq, from: this.id, gossip: this.collectGossip() });
        break;
      case 'ack': {
        const probe = this.pending.get(msg.seq);
        if (probe) {
          this.pending.delete(msg.seq);
          if (probe.origin !== null) {
            ctx.send(probe.origin, {
              kind: 'ack',
              seq: probe.originSeq,
              from: this.id,
              gossip: this.collectGossip(),
            });
          }
        }
        break;
      }
      case 'ping-req': {
        if (msg.target === this.id) {
          ctx.send(msg.from, { kind: 'ack', seq: msg.seq, from: this.id, gossip: this.collectGossip() });
        } else if (this.members.has(msg.target)) {
          const seq = this.nextSeq++;
          this.pending.set(seq, {
            target: msg.target,
            sentAt: ctx.now,
            indirectAt: -1,
            origin: msg.from,
            originSeq: msg.seq,
          });
          ctx.send(msg.target, { kind: 'ping', seq, from: this.id, gossip: this.collectGossip() });
        }
        break;
      }
    }
  }

  private probeTargets(): string[] {
    const out: string[] = [];
    for (const [id, member] of this.members) {
      if (member.status !== 'dead') out.push(id);
    }
    return out;
  }

  private startProbe(target: string, ctx: NodeContext): void {
    const seq = this.nextSeq++;
    this.pending.set(seq, { target, sentAt: ctx.now, indirectAt: -1, origin: null, originSeq: 0 });
    ctx.send(target, { kind: 'ping', seq, from: this.id, gossip: this.collectGossip() });
  }

  /**
   * Rejoin probe to a dead member. Carries the local dead-list plus our own
   * alive record, so the other side (if actually alive) is forced to refute
   * its alleged death with a bumped incarnation.
   */
  private rejoinProbe(target: string, ctx: NodeContext): void {
    const seq = this.nextSeq++;
    this.pending.set(seq, { target, sentAt: ctx.now, indirectAt: -1, origin: null, originSeq: 0 });
    const gossip: MemberUpdate[] = [
      { id: this.id, incarnation: this.ownIncarnation, status: 'alive' },
    ];
    for (const [id, member] of this.members) {
      if (member.status === 'dead') {
        gossip.push({ id, incarnation: member.incarnation, status: 'dead' });
      }
    }
    for (const update of this.collectGossip()) {
      if (gossip.length < this.config.gossipMax) gossip.push(update);
    }
    ctx.send(target, { kind: 'ping', seq, from: this.id, gossip });
  }

  private failProbe(seq: number, probe: PendingProbe, ctx: NodeContext): void {
    this.pending.delete(seq);
    const member = this.members.get(probe.target);
    if (member && member.status === 'alive') {
      member.status = 'suspect';
      member.suspectSince = ctx.now;
      ctx.log(`t=${ctx.now} ${this.id} suspect ${probe.target} inc=${member.incarnation}`);
      this.addGossip({ id: probe.target, incarnation: member.incarnation, status: 'suspect' });
    }
  }

  private applyGossip(updates: readonly MemberUpdate[], ctx: NodeContext): void {
    for (const update of updates) this.applyUpdate(update, ctx);
  }

  private applyUpdate(update: MemberUpdate, ctx: NodeContext): void {
    const now = ctx.now;

    if (update.id === this.id) {
      // Arbitration: never accept our own death/suspicion from stale gossip.
      if (update.status !== 'alive' && update.incarnation >= this.ownIncarnation) {
        this.ownIncarnation++;
        ctx.log(`t=${now} ${this.id} refute ${update.status} inc=${this.ownIncarnation}`);
        this.addGossip({ id: this.id, incarnation: this.ownIncarnation, status: 'alive' });
      }
      return;
    }

    const current = this.members.get(update.id);
    if (!current) {
      this.members.set(update.id, {
        incarnation: update.incarnation,
        status: update.status,
        suspectSince: update.status === 'suspect' ? now : -1,
      });
      this.addGossip(update);
      return;
    }

    const newer =
      update.incarnation > current.incarnation ||
      (update.incarnation === current.incarnation &&
        STATUS_RANK[update.status] > STATUS_RANK[current.status]);
    if (!newer) return;

    const previous = current.status;
    current.incarnation = update.incarnation;
    current.status = update.status;
    current.suspectSince = update.status === 'suspect' ? now : -1;
    if (update.status !== previous) {
      if (update.status === 'suspect') {
        ctx.log(`t=${now} ${this.id} suspect ${update.id} inc=${update.incarnation}`);
      } else if (update.status === 'dead') {
        ctx.log(`t=${now} ${this.id} declare-dead ${update.id} inc=${update.incarnation}`);
      } else {
        ctx.log(`t=${now} ${this.id} alive ${update.id} inc=${update.incarnation}`);
      }
    }
    this.addGossip(update);
  }

  private addGossip(update: MemberUpdate): void {
    const existing = this.gossip.find((entry) => entry.update.id === update.id);
    if (existing) {
      const newer =
        update.incarnation > existing.update.incarnation ||
        (update.incarnation === existing.update.incarnation &&
          STATUS_RANK[update.status] >= STATUS_RANK[existing.update.status]);
      if (newer) {
        existing.update = update;
        existing.remaining = this.config.gossipRetransmit;
      }
    } else {
      this.gossip.push({ update, remaining: this.config.gossipRetransmit });
    }
  }

  private collectGossip(): MemberUpdate[] {
    const out: MemberUpdate[] = [];
    for (const entry of this.gossip) {
      if (entry.remaining <= 0 || out.length >= this.config.gossipMax) continue;
      out.push(entry.update);
      entry.remaining--;
    }
    return out;
  }
}
