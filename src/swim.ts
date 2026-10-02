import type { Simulator } from './simulator.ts';
import type { Network } from './network.ts';
import type { Random } from './rng.ts';

export type MemberState = 'alive' | 'suspect' | 'dead';

export interface MemberRecord {
  id: string;
  state: MemberState;
  incarnation: number;
  /** Node that produced this record (self for alive, accuser for suspect/dead). */
  declaredBy: string;
}

const STATE_RANK: Record<MemberState, number> = { alive: 0, suspect: 1, dead: 2 };

/**
 * Deterministic arbitration between two conflicting records about the
 * same member. Total order, no wall-clock, no Math.random:
 *
 *   1. higher incarnation wins;
 *   2. tie  => worse state wins (dead > suspect > alive);
 *   3. tie  => lexicographically larger declaredBy wins.
 *
 * Because the order is total and symmetric, every node that sees the
 * same set of records converges to the exact same winner.
 */
export function arbitrate(current: MemberRecord, incoming: MemberRecord): MemberRecord {
  if (incoming.incarnation !== current.incarnation) {
    return incoming.incarnation > current.incarnation ? incoming : current;
  }
  if (STATE_RANK[incoming.state] !== STATE_RANK[current.state]) {
    return STATE_RANK[incoming.state] > STATE_RANK[current.state] ? incoming : current;
  }
  return incoming.declaredBy > current.declaredBy ? incoming : current;
}

export type SwimMessage =
  | { kind: 'ping'; from: string; seq: number; relayTo?: string; gossip: MemberRecord[] }
  | { kind: 'ack'; from: string; seq: number; gossip: MemberRecord[] }
  | { kind: 'ping-req'; from: string; target: string; seq: number; gossip: MemberRecord[] }
  | { kind: 'gossip'; from: string; gossip: MemberRecord[] };

export interface SwimOptions {
  /** Ticks between protocol periods. */
  protocolPeriod: number;
  /** Ticks to wait for a direct ack before going indirect. */
  ackTimeout: number;
  /** Ticks a member stays suspect before being declared dead. */
  suspicionTimeout: number;
  /** Number of ping-req relays used in the indirect phase. */
  indirectPings: number;
  /** How the ping target is chosen each period. */
  targetSelection?: 'random' | 'round-robin';
}

/**
 * SWIM failure detector + dissemination, driven entirely by the
 * Simulator's virtual clock and the seeded RNG.
 */
export class SwimNode {
  readonly id: string;
  private sim: Simulator;
  private network: Network;
  private rng: Random;
  private opts: Required<SwimOptions>;
  private members: Map<string, MemberRecord> = new Map();
  private incarnation: number = 0;
  private seq: number = 0;
  private rrCounter: number = 0;
  /** seq -> ping target, for probes awaiting an ack. */
  private pending: Map<number, string> = new Map();

  constructor(
    id: string,
    peers: string[],
    sim: Simulator,
    network: Network,
    rng: Random,
    opts: SwimOptions,
  ) {
    this.id = id;
    this.sim = sim;
    this.network = network;
    this.rng = rng;
    this.opts = { targetSelection: 'random', ...opts };
    this.members.set(id, { id, state: 'alive', incarnation: 0, declaredBy: id });
    for (const peer of peers) {
      this.members.set(peer, { id: peer, state: 'alive', incarnation: 0, declaredBy: peer });
    }
    network.register(id, (msg) => this.receive(msg as SwimMessage));
    sim.schedule(0, () => this.protocolTick());
  }

  /** Deterministic, sorted membership snapshot (for assertions). */
  snapshot(): MemberRecord[] {
    return [...this.members.values()]
      .map((rec) => ({ ...rec }))
      .sort((a, b) => (a.id < b.id ? -1 : 1));
  }

  getMember(id: string): MemberRecord | undefined {
    const rec = this.members.get(id);
    return rec ? { ...rec } : undefined;
  }

  // ---- protocol period -------------------------------------------------

  private protocolTick(): void {
    this.probe();
    this.antiEntropy();
    this.sim.schedule(this.opts.protocolPeriod, () => this.protocolTick());
  }

  private candidates(): string[] {
    const out: string[] = [];
    for (const [id, rec] of this.members) {
      if (id !== this.id && rec.state !== 'dead') out.push(id);
    }
    return out.sort();
  }

  private probe(): void {
    const candidates = this.candidates();
    if (candidates.length === 0) return;
    let target: string;
    if (this.opts.targetSelection === 'round-robin') {
      target = candidates[this.rrCounter % candidates.length];
      this.rrCounter++;
    } else {
      target = candidates[this.rng.int(candidates.length)];
    }
    const seq = this.seq++;
    this.pending.set(seq, target);
    this.send(target, { kind: 'ping', from: this.id, seq, gossip: this.gossipTable() });
    this.sim.schedule(this.opts.ackTimeout, () => {
      if (this.pending.has(seq)) this.goIndirect(seq, target);
    });
    this.sim.schedule(this.opts.protocolPeriod, () => {
      if (this.pending.delete(seq)) this.markSuspect(target);
    });
  }

  private goIndirect(seq: number, target: string): void {
    const relays = this.candidates().filter((id) => id !== target);
    for (let i = 0; i < this.opts.indirectPings && relays.length > 0; i++) {
      const relay = relays.splice(this.rng.int(relays.length), 1)[0];
      this.send(relay, { kind: 'ping-req', from: this.id, target, seq, gossip: this.gossipTable() });
    }
  }

  /** Periodic full-table anti-entropy broadcast (also reaches "dead" members). */
  private antiEntropy(): void {
    const gossip = this.gossipTable();
    for (const id of this.members.keys()) {
      if (id !== this.id) this.send(id, { kind: 'gossip', from: this.id, gossip });
    }
  }

  // ---- message handling ------------------------------------------------

  receive(msg: SwimMessage): void {
    for (const rec of msg.gossip) this.merge(rec);
    switch (msg.kind) {
      case 'ping': {
        const dest = msg.relayTo ?? msg.from;
        this.send(dest, { kind: 'ack', from: this.id, seq: msg.seq, gossip: this.gossipTable() });
        break;
      }
      case 'ack': {
        this.pending.delete(msg.seq);
        break;
      }
      case 'ping-req': {
        this.send(msg.target, {
          kind: 'ping',
          from: this.id,
          seq: msg.seq,
          relayTo: msg.from,
          gossip: this.gossipTable(),
        });
        break;
      }
      case 'gossip':
        break;
    }
  }

  // ---- membership state machine ----------------------------------------

  private markSuspect(id: string): void {
    const rec = this.members.get(id);
    if (!rec || rec.state !== 'alive') return;
    const suspect: MemberRecord = {
      id,
      state: 'suspect',
      incarnation: rec.incarnation,
      declaredBy: this.id,
    };
    this.members.set(id, suspect);
    this.sim.log(`${this.id} DECLARE_SUSPECT ${id} inc=${suspect.incarnation}`);
    this.armSuspicion(id, suspect.incarnation);
  }

  private armSuspicion(id: string, incarnation: number): void {
    this.sim.schedule(this.opts.suspicionTimeout, () => {
      const cur = this.members.get(id);
      if (cur && cur.state === 'suspect' && cur.incarnation === incarnation) {
        this.members.set(id, { id, state: 'dead', incarnation, declaredBy: this.id });
        this.sim.log(`${this.id} DECLARE_DEAD ${id} inc=${incarnation}`);
      }
    });
  }

  /** Merge one gossiped record into the local table via `arbitrate`. */
  merge(incoming: MemberRecord): void {
    if (incoming.id === this.id) {
      // Bad news about ourselves: refute by bumping our incarnation.
      if (incoming.state !== 'alive' && incoming.incarnation >= this.incarnation) {
        this.incarnation = incoming.incarnation + 1;
        this.members.set(this.id, {
          id: this.id,
          state: 'alive',
          incarnation: this.incarnation,
          declaredBy: this.id,
        });
        this.sim.log(`${this.id} REFUTE inc=${this.incarnation}`);
      }
      return;
    }
    const current = this.members.get(incoming.id);
    if (!current) {
      this.members.set(incoming.id, { ...incoming });
      return;
    }
    const winner = arbitrate(current, incoming);
    if (winner !== incoming) return;
    if (winner.state === current.state && winner.incarnation === current.incarnation) return;
    this.members.set(incoming.id, { ...incoming });
    if (current.state === 'dead' && incoming.state === 'alive') {
      this.sim.log(`${this.id} REVIVE ${incoming.id} inc=${incoming.incarnation}`);
    }
    if (incoming.state === 'suspect') {
      this.armSuspicion(incoming.id, incoming.incarnation);
    }
  }

  // ---- helpers ----------------------------------------------------------

  private gossipTable(): MemberRecord[] {
    return [...this.members.values()].map((rec) => ({ ...rec }));
  }

  private send(to: string, msg: SwimMessage): void {
    this.network.send(this.id, to, msg);
  }
}
