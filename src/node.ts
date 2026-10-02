import { createHash } from 'node:crypto';
import {
  compareBallot,
  ballotKey,
  valueEquals,
  ZERO_BALLOT,
  NOOP,
  GENESIS_HASH,
} from './types.ts';
import type { Ballot, LogEntry, Message, NodeId, Value } from './types.ts';
import type { Network } from './network.ts';
import type { Clock } from './clock.ts';
import type { InvariantMonitor } from './invariants.ts';

export const LEASE_MS = 1_000;

export function hashEntry(slot: number, ballot: Ballot, value: Value, prevHash: string): string {
  return createHash('sha1').update(JSON.stringify([slot, ballot, value, prevHash])).digest('hex');
}

interface Collector {
  expected: number;
  responses: number;
  ok: number;
  done: boolean;
  accepted: Map<number, LogEntry>;
  resolve: (ok: boolean) => void;
}

/**
 * Multi-Paxos node: acceptor + proposer + learner.
 *
 * - Phase 1 (Prepare/Promise) elects a leader and learns accepted state.
 * - Phase 2 (Accept/Accepted) replicates entries to a majority.
 * - A prepared leader with a valid lease skips Phase 1 for new slots.
 * - A new leader back-fills every slot up to the highest seen slot,
 *   using the highest-ballot accepted value or a no-op, so the
 *   committed prefix never contains holes.
 */
export class PaxosNode {
  monitor: InvariantMonitor | null = null;

  promised: Ballot = ZERO_BALLOT;
  readonly log = new Map<number, LogEntry>();
  readonly committed = new Map<number, LogEntry>();
  commitIndex = 0;

  leaderBallot: Ballot | null = null;
  prepared = false;
  leaseUntil = 0;

  private round = 0;
  private nextSlot = 1;
  private collectors = new Map<string, Collector>();
  private lastCommitSender: NodeId | null = null;
  readonly id: NodeId;
  private net: Network;
  private clock: Clock;
  private quorum: number;

  constructor(id: NodeId, net: Network, clock: Clock, quorum: number) {
    this.id = id;
    this.net = net;
    this.clock = clock;
    this.quorum = quorum;
  }

  leaseValid(): boolean {
    return this.prepared && this.clock.now() < this.leaseUntil;
  }

  pendingOperations(): number {
    return this.collectors.size;
  }

  // ----------------------------------------------------------- proposer

  /** Propose a value; returns the committed slot. Runs Phase 1 first unless a valid lease is held. */
  async propose(value: Value): Promise<number> {
    if (!this.leaseValid()) {
      const ok = await this.prepare();
      if (!ok) throw new Error(`node ${this.id}: cannot become leader (no quorum)`);
    }
    const slot = this.nextSlot++;
    const ok = await this.acceptPhase(slot, this.leaderBallot as Ballot, value);
    if (!ok) throw new Error(`node ${this.id}: accept phase failed for slot ${slot}`);
    return slot;
  }

  /** Phase 1: gather promises from a quorum, then back-fill every known slot via Phase 2. */
  async prepare(): Promise<boolean> {
    this.round = Math.max(this.round + 1, this.promised.round + 1);
    const ballot: Ballot = { round: this.round, nodeId: this.id };
    if (this.net.reachableCount(this.id) < this.quorum) return false;

    const key = `P:${ballotKey(ballot)}`;
    const collector = this.newCollector(key);
    this.net.send({ type: 'Prepare', from: this.id, ballot });
    this.onPrepare({ type: 'Prepare', from: this.id, ballot });
    if (!(await collector.promise)) return false;

    // Committed decisions are ground truth and always win.
    for (const [slot, entry] of this.committed) collector.state.accepted.set(slot, entry);

    let maxSlot = 0;
    for (const slot of collector.state.accepted.keys()) maxSlot = Math.max(maxSlot, slot);

    // Back-fill holes and re-propose inherited values before serving new ones.
    for (let slot = 1; slot <= maxSlot; slot++) {
      const value = collector.state.accepted.get(slot)?.value ?? NOOP;
      const ok = await this.acceptPhase(slot, ballot, value);
      if (!ok) return false;
    }

    this.leaderBallot = ballot;
    this.prepared = true;
    this.leaseUntil = this.clock.now() + LEASE_MS;
    this.nextSlot = maxSlot + 1;
    return true;
  }

  /** Phase 2: replicate one entry to a quorum, then commit and broadcast. */
  private async acceptPhase(slot: number, ballot: Ballot, value: Value): Promise<boolean> {
    const prev = slot > 1 ? this.log.get(slot - 1) : undefined;
    const prevHash = prev ? prev.hash : GENESIS_HASH;
    const entry: LogEntry = { slot, ballot, value, prevHash, hash: '' };
    entry.hash = hashEntry(slot, ballot, value, prevHash);

    const key = `A:${slot}:${ballotKey(ballot)}`;
    const collector = this.newCollector(key);
    this.net.send({ type: 'Accept', from: this.id, entry });
    this.onAccept({ type: 'Accept', from: this.id, entry });
    const ok = await collector.promise;
    if (ok) {
      this.commitEntry(entry);
      this.net.send({ type: 'Commit', from: this.id, entry });
    }
    return ok;
  }

  // ----------------------------------------------------------- acceptor

  receive(msg: Message): void {
    switch (msg.type) {
      case 'Prepare':
        this.onPrepare(msg);
        break;
      case 'Promise':
        this.onPromise(msg);
        break;
      case 'PrepareNack':
        this.onPrepareNack(msg);
        break;
      case 'Accept':
        this.onAccept(msg);
        break;
      case 'Accepted':
        this.tally(`A:${msg.slot}:${ballotKey(msg.ballot)}`, true);
        break;
      case 'AcceptNack':
        this.round = Math.max(this.round, msg.promised.round);
        if (this.leaderBallot && compareBallot(msg.promised, this.leaderBallot) > 0) this.stepDown();
        this.tally(`A:${msg.slot}:${ballotKey(msg.ballot)}`, false);
        break;
      case 'Commit':
        this.onCommit(msg);
        break;
      case 'SyncRequest':
        this.onSyncRequest(msg);
        break;
      case 'SyncResponse':
        this.onSyncResponse(msg);
        break;
    }
  }

  private onPrepare(msg: Extract<Message, { type: 'Prepare' }>): void {
    if (compareBallot(msg.ballot, this.promised) > 0) {
      this.promiseBallot(msg.ballot);
      this.net.send({
        type: 'Promise',
        from: this.id,
        to: msg.from,
        ballot: msg.ballot,
        accepted: [...this.log.values()],
      });
    } else {
      this.net.send({
        type: 'PrepareNack',
        from: this.id,
        to: msg.from,
        ballot: msg.ballot,
        promised: this.promised,
      });
    }
  }

  private onPromise(msg: Extract<Message, { type: 'Promise' }>): void {
    const collector = this.collectors.get(`P:${ballotKey(msg.ballot)}`);
    if (collector && !collector.done) {
      for (const entry of msg.accepted) {
        const cur = collector.accepted.get(entry.slot);
        if (!cur || compareBallot(entry.ballot, cur.ballot) > 0) {
          collector.accepted.set(entry.slot, entry);
        }
      }
    }
    this.tally(`P:${ballotKey(msg.ballot)}`, true);
  }

  private onPrepareNack(msg: Extract<Message, { type: 'PrepareNack' }>): void {
    this.round = Math.max(this.round, msg.promised.round);
    this.tally(`P:${ballotKey(msg.ballot)}`, false);
  }

  private onAccept(msg: Extract<Message, { type: 'Accept' }>): void {
    const entry = msg.entry;
    const nack = (): void =>
      this.net.send({
        type: 'AcceptNack',
        from: this.id,
        to: msg.from,
        slot: entry.slot,
        ballot: entry.ballot,
        promised: this.promised,
      });

    if (compareBallot(entry.ballot, this.promised) < 0) {
      nack();
      return;
    }
    if (hashEntry(entry.slot, entry.ballot, entry.value, entry.prevHash) !== entry.hash) {
      this.monitor?.onIntegrity(this.id, entry);
      nack();
      return;
    }
    if (compareBallot(entry.ballot, this.promised) > 0) this.promiseBallot(entry.ballot);

    const committedEntry = this.committed.get(entry.slot);
    if (committedEntry && !valueEquals(committedEntry.value, entry.value)) {
      this.monitor?.onCommittedConflict(this.id, entry.slot, committedEntry, entry);
      nack();
      return;
    }
    const existing = this.log.get(entry.slot);
    if (existing && existing.hash !== entry.hash) {
      this.monitor?.onOverwrite(this.id, entry.slot, existing, entry, committedEntry !== undefined);
    }
    this.log.set(entry.slot, entry);
    if (committedEntry) this.committed.set(entry.slot, entry);
    this.net.send({ type: 'Accepted', from: this.id, to: msg.from, slot: entry.slot, ballot: entry.ballot });
  }

  // ------------------------------------------------------------ learner

  private onCommit(msg: Extract<Message, { type: 'Commit' }>): void {
    const entry = msg.entry;
    if (hashEntry(entry.slot, entry.ballot, entry.value, entry.prevHash) !== entry.hash) {
      this.monitor?.onIntegrity(this.id, entry);
      return;
    }
    this.lastCommitSender = msg.from;
    // Committed entries are ground truth: overwriting a local uncommitted
    // entry is always legitimate (that is how dirty minority logs are
    // repaired). Conflicts between two *committed* values are caught by
    // commitEntry below.
    this.log.set(entry.slot, entry);
    this.commitEntry(entry);
    // Hole repair: pull any committed entries we missed.
    if (this.commitIndex < entry.slot - 1) {
      this.net.send({ type: 'SyncRequest', from: this.id, to: msg.from, fromSlot: this.commitIndex + 1 });
    }
  }

  private onSyncRequest(msg: Extract<Message, { type: 'SyncRequest' }>): void {
    const entries = [...this.committed.values()]
      .filter((e) => e.slot >= msg.fromSlot)
      .sort((a, b) => a.slot - b.slot);
    if (entries.length > 0) {
      this.net.send({ type: 'SyncResponse', from: this.id, to: msg.from, entries });
    }
  }

  private onSyncResponse(msg: Extract<Message, { type: 'SyncResponse' }>): void {
    this.lastCommitSender = msg.from;
    for (const entry of msg.entries) {
      if (hashEntry(entry.slot, entry.ballot, entry.value, entry.prevHash) !== entry.hash) {
        this.monitor?.onIntegrity(this.id, entry);
        continue;
      }
      this.log.set(entry.slot, entry);
      this.commitEntry(entry);
    }
  }

  private commitEntry(entry: LogEntry): void {
    const prev = this.committed.get(entry.slot);
    if (prev) {
      if (!valueEquals(prev.value, entry.value)) {
        this.monitor?.onCommittedConflict(this.id, entry.slot, prev, entry);
      } else if (prev.hash !== entry.hash && compareBallot(entry.ballot, prev.ballot) > 0) {
        // Same decided value re-accepted under a newer ballot: adopt the
        // newer entry so the local hash chain matches the leader's.
        this.monitor?.onOverwrite(this.id, entry.slot, prev, entry, true);
        this.committed.set(entry.slot, entry);
        this.log.set(entry.slot, entry);
        this.repairCommittedChain();
      }
      return;
    }
    this.monitor?.onCommit(this.id, entry.slot, entry);
    this.committed.set(entry.slot, entry);
    while (this.committed.has(this.commitIndex + 1)) this.commitIndex++;
    this.repairCommittedChain();
  }

  /** Walk the committed prefix; if the hash chain is broken, pull the truth from a peer. */
  private repairCommittedChain(): void {
    for (let slot = this.commitIndex; slot > 1; slot--) {
      const entry = this.committed.get(slot);
      const prev = this.committed.get(slot - 1);
      if (entry && prev && prev.hash !== entry.prevHash) {
        const peer = this.lastCommitSender;
        if (peer) {
          this.net.send({ type: 'SyncRequest', from: this.id, to: peer, fromSlot: slot - 1 });
        }
        return;
      }
    }
  }

  // ------------------------------------------------------------ helpers

  private promiseBallot(ballot: Ballot): void {
    const old = this.promised;
    this.promised = ballot;
    this.monitor?.onPromise(this.id, old, ballot);
    if (this.leaderBallot && compareBallot(ballot, this.leaderBallot) > 0) this.stepDown();
  }

  private stepDown(): void {
    this.prepared = false;
    this.leaseUntil = 0;
  }

  onTopologyChange(): void {
    for (const [key, collector] of [...this.collectors]) {
      this.finishCollector(key, collector, false);
    }
  }

  private newCollector(key: string): { promise: Promise<boolean>; state: Collector } {
    const state: Collector = {
      expected: this.net.reachableCount(this.id),
      responses: 0,
      ok: 0,
      done: false,
      accepted: new Map(),
      resolve: () => undefined,
    };
    const promise = new Promise<boolean>((resolve) => {
      state.resolve = resolve;
    });
    this.collectors.set(key, state);
    return { promise, state };
  }

  private tally(key: string, ok: boolean): void {
    const collector = this.collectors.get(key);
    if (!collector || collector.done) return;
    collector.responses++;
    if (ok) collector.ok++;
    if (collector.ok >= this.quorum) this.finishCollector(key, collector, true);
    else if (collector.responses >= collector.expected) this.finishCollector(key, collector, false);
  }

  private finishCollector(key: string, collector: Collector, result: boolean): void {
    collector.done = true;
    this.collectors.delete(key);
    collector.resolve(result);
  }
}
