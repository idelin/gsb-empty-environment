import { ManualClock } from './clock.ts';
import { Network } from './transport.ts';

/** Ballot = (round, nodeId), totally ordered. */
export interface Ballot {
  round: number;
  nodeId: string;
}

export function compareBallots(a: Ballot, b: Ballot): number {
  if (a.round !== b.round) return a.round - b.round;
  return a.nodeId < b.nodeId ? -1 : a.nodeId > b.nodeId ? 1 : 0;
}

export interface LogEntry {
  kind: 'noop' | 'app';
  payload?: unknown;
}

export interface SlotRecord {
  ballot: Ballot;
  entry: LogEntry;
}

export type Message =
  | { type: 'prepare'; from: string; to: string; ballot: Ballot }
  | {
      type: 'promise';
      from: string;
      to: string;
      ballot: Ballot;
      ok: boolean;
      promised: Ballot | null;
      accepted: [number, SlotRecord][];
      commitIndex: number;
    }
  | { type: 'accept'; from: string; to: string; ballot: Ballot; slot: number; entry: LogEntry }
  | { type: 'accepted'; from: string; to: string; ballot: Ballot; slot: number; ok: boolean; promised: Ballot | null }
  | { type: 'commit'; from: string; to: string; commitIndex: number; records: [number, SlotRecord][] }
  | { type: 'sync-request'; from: string; to: string }
  | { type: 'sync-response'; from: string; to: string; records: [number, SlotRecord][]; commitIndex: number };

export class NoMajorityError extends Error {
  constructor(msg: string) {
    super(msg);
    this.name = 'NoMajorityError';
  }
}

export class NotCommittedError extends Error {
  constructor(msg: string) {
    super(msg);
    this.name = 'NotCommittedError';
  }
}

type Role = 'follower' | 'leader';

export class PaxosNode {
  readonly id: string;
  private readonly peers: string[];
  private readonly net: Network;
  private readonly clock: ManualClock;
  private readonly majority: number;
  private readonly leaseDuration: number;

  // Persistent acceptor state.
  promised: Ballot | null = null;
  readonly log = new Map<number, SlotRecord>();
  commitIndex = 0;
  readonly applied: LogEntry[] = [];

  // Volatile leader state.
  role: Role = 'follower';
  private leaderBallot: Ballot | null = null;
  private leaseUntil = -1;
  private highestRoundSeen = 0;
  private promiseCount = 0;
  private collectedAccepted = new Map<number, SlotRecord>();
  private collectedCommitIndex = 0;
  private acceptors = new Map<number, Set<string>>();
  private chosen = new Set<number>();
  private nextSlot = 1;

  // Instrumentation for tests / leak assertions.
  prepareCount = 0;
  acceptCount = 0;
  pendingCampaigns = 0;

  /** Fired for every entry applied in commit order (slot = applied.length). */
  onApply: ((entry: LogEntry, slot: number) => void) | null = null;

  constructor(
    id: string,
    peers: string[],
    net: Network,
    clock: ManualClock,
    leaseDuration = 100,
  ) {
    this.id = id;
    this.peers = peers;
    this.net = net;
    this.clock = clock;
    this.leaseDuration = leaseDuration;
    this.majority = Math.floor((peers.length + 1) / 2) + 1;
    net.register(id, (msg) => this.receive(msg as Message));
  }

  private allIds(): string[] {
    return [this.id, ...this.peers];
  }

  private send(msg: Message): void {
    this.net.send(msg);
  }

  // ---------------------------------------------------------------- Phase 1

  /**
   * Run Phase 1 (Prepare/Promise). On a majority of promises this node
   * becomes leader, adopts the highest-ballot accepted value per slot and
   * fills every hole below the highest known slot with a no-op (Phase 2
   * repair), so the log can always commit contiguously.
   */
  campaign(): boolean {
    const ballot: Ballot = { round: this.highestRoundSeen + 1, nodeId: this.id };
    this.highestRoundSeen = ballot.round;
    this.promiseCount = 0;
    this.collectedAccepted = new Map();
    this.collectedCommitIndex = 0;
    this.pendingCampaigns++;
    this.prepareCount++;
    try {
      for (const to of this.allIds()) {
        this.send({ type: 'prepare', from: this.id, to, ballot });
      }
      this.net.flush();
    } finally {
      this.pendingCampaigns--;
    }
    if (this.promiseCount < this.majority) return false;

    this.role = 'leader';
    this.leaderBallot = ballot;
    this.leaseUntil = this.clock.now() + this.leaseDuration;
    this.acceptors = new Map();
    this.repairLog();
    return true;
  }

  /** Hole repair: re-propose learned values, fill gaps with no-ops. */
  private repairLog(): void {
    const base = Math.max(this.commitIndex, this.collectedCommitIndex);
    let maxSlot = base;
    for (const slot of this.collectedAccepted.keys()) maxSlot = Math.max(maxSlot, slot);
    for (const slot of this.log.keys()) maxSlot = Math.max(maxSlot, slot);

    for (let slot = 1; slot <= maxSlot; slot++) {
      const learned = this.highestRecordFor(slot);
      if (slot <= base) {
        // Already chosen by a previous majority: adopt and mark chosen.
        if (learned) this.log.set(slot, learned);
        this.chosen.add(slot);
        continue;
      }
      const entry: LogEntry = learned ? learned.entry : { kind: 'noop' };
      this.runPhase2(slot, entry);
    }
    this.nextSlot = maxSlot + 1;
    this.advanceCommit();
  }

  private highestRecordFor(slot: number): SlotRecord | null {
    let best: SlotRecord | null = null;
    for (const rec of [this.log.get(slot), this.collectedAccepted.get(slot)]) {
      if (rec && (!best || compareBallots(rec.ballot, best.ballot) > 0)) best = rec;
    }
    return best;
  }

  // ---------------------------------------------------------------- Phase 2

  /**
   * Propose an application entry. With a valid leader lease this skips
   * Phase 1 entirely (Multi-Paxos fast path) and commits in one round trip.
   */
  propose(entry: LogEntry): number {
    if (!this.hasValidLease()) {
      if (!this.campaign()) {
        throw new NoMajorityError(`${this.id}: cannot gather a majority`);
      }
    }
    const slot = this.nextSlot++;
    this.runPhase2(slot, entry);
    this.advanceCommit();
    if (!this.chosen.has(slot)) {
      throw new NotCommittedError(`${this.id}: slot ${slot} did not reach a majority`);
    }
    return slot;
  }

  hasValidLease(): boolean {
    return this.role === 'leader' && this.clock.now() < this.leaseUntil;
  }

  get isLeader(): boolean {
    return this.role === 'leader';
  }

  /** Send Accept for (slot, entry) under the current leader ballot. */
  private runPhase2(slot: number, entry: LogEntry): void {
    const ballot = this.leaderBallot;
    if (!ballot) throw new Error('runPhase2 requires leadership');
    this.acceptCount++;
    for (const to of this.allIds()) {
      this.send({ type: 'accept', from: this.id, to, ballot, slot, entry });
    }
    this.net.flush();
  }

  private advanceCommit(): void {
    let advanced = false;
    while (this.chosen.has(this.commitIndex + 1) && this.log.has(this.commitIndex + 1)) {
      this.commitIndex++;
      const entry = this.log.get(this.commitIndex)!.entry;
      this.applied.push(entry);
      this.onApply?.(entry, this.commitIndex);
      advanced = true;
    }
    if (advanced) {
      for (const to of this.allIds()) {
        if (to === this.id) continue;
        this.send({
          type: 'commit',
          from: this.id,
          to,
          commitIndex: this.commitIndex,
          records: this.chosenRecords(),
        });
      }
      this.net.flush();
    }
  }

  /** All chosen records at or below commitIndex (piggybacked on commit). */
  private chosenRecords(): [number, SlotRecord][] {
    const records: [number, SlotRecord][] = [];
    for (let slot = 1; slot <= this.commitIndex; slot++) {
      const rec = this.log.get(slot);
      if (rec) records.push([slot, rec]);
    }
    return records;
  }

  // -------------------------------------------------------------- Handlers

  private receive(msg: Message): void {
    switch (msg.type) {
      case 'prepare':
        this.onPrepare(msg);
        break;
      case 'promise':
        this.onPromise(msg);
        break;
      case 'accept':
        this.onAccept(msg);
        break;
      case 'accepted':
        this.onAccepted(msg);
        break;
      case 'commit':
        this.onCommit(msg);
        break;
      case 'sync-request':
        this.onSyncRequest(msg);
        break;
      case 'sync-response':
        this.onSyncResponse(msg);
        break;
    }
  }

  private stepDownIfNeeded(ballot: Ballot): void {
    if (this.role === 'leader' && this.leaderBallot && compareBallots(ballot, this.leaderBallot) > 0) {
      this.role = 'follower';
      this.leaderBallot = null;
      this.leaseUntil = -1;
    }
  }

  private onPrepare(msg: { from: string; ballot: Ballot }): void {
    this.highestRoundSeen = Math.max(this.highestRoundSeen, msg.ballot.round);
    const ok = !this.promised || compareBallots(msg.ballot, this.promised) > 0;
    if (ok) {
      this.stepDownIfNeeded(msg.ballot);
      this.promised = msg.ballot;
    }
    this.send({
      type: 'promise',
      from: this.id,
      to: msg.from,
      ballot: msg.ballot,
      ok,
      promised: this.promised,
      accepted: ok ? [...this.log.entries()] : [],
      commitIndex: this.commitIndex,
    });
  }

  private onPromise(msg: {
    ballot: Ballot;
    ok: boolean;
    promised: Ballot | null;
    accepted: [number, SlotRecord][];
    commitIndex: number;
  }): void {
    if (msg.promised) this.highestRoundSeen = Math.max(this.highestRoundSeen, msg.promised.round);
    if (this.role === 'leader') return; // already elected for a later ballot
    if (!this.leaderBallotCandidate(msg.ballot)) return;
    if (!msg.ok) return;
    this.promiseCount++;
    this.collectedCommitIndex = Math.max(this.collectedCommitIndex, msg.commitIndex);
    for (const [slot, rec] of msg.accepted) {
      const cur = this.collectedAccepted.get(slot);
      if (!cur || compareBallots(rec.ballot, cur.ballot) > 0) {
        this.collectedAccepted.set(slot, rec);
      }
    }
  }

  private leaderBallotCandidate(ballot: Ballot): boolean {
    return ballot.nodeId === this.id && ballot.round === this.highestRoundSeen;
  }

  private onAccept(msg: { from: string; ballot: Ballot; slot: number; entry: LogEntry }): void {
    this.highestRoundSeen = Math.max(this.highestRoundSeen, msg.ballot.round);
    const ok = !this.promised || compareBallots(msg.ballot, this.promised) >= 0;
    if (ok) {
      this.stepDownIfNeeded(msg.ballot);
      this.promised = msg.ballot;
      this.log.set(msg.slot, { ballot: msg.ballot, entry: msg.entry });
    }
    this.send({
      type: 'accepted',
      from: this.id,
      to: msg.from,
      ballot: msg.ballot,
      slot: msg.slot,
      ok,
      promised: this.promised,
    });
  }

  private onAccepted(msg: { ballot: Ballot; slot: number; ok: boolean; promised: Ballot | null; from: string }): void {
    if (msg.promised) this.highestRoundSeen = Math.max(this.highestRoundSeen, msg.promised.round);
    if (this.role !== 'leader' || !this.leaderBallot) return;
    if (compareBallots(msg.ballot, this.leaderBallot) !== 0) return;
    if (!msg.ok) {
      // A acceptor promised a higher ballot: our leadership is stale.
      this.stepDownIfNeeded(msg.promised!);
      return;
    }
    let set = this.acceptors.get(msg.slot);
    if (!set) {
      set = new Set();
      this.acceptors.set(msg.slot, set);
    }
    set.add(msg.from);
    if (set.size >= this.majority && !this.chosen.has(msg.slot)) {
      this.chosen.add(msg.slot);
      this.advanceCommit();
    }
  }

  private onCommit(msg: { from: string; commitIndex: number; records: [number, SlotRecord][] }): void {
    if (msg.commitIndex <= this.commitIndex) return;
    // Adopt the leader's chosen records: they safely overwrite any local
    // uncommitted ("dirty") records at those slots.
    for (const [slot, rec] of msg.records) {
      if (slot > this.commitIndex && slot <= msg.commitIndex) {
        this.log.set(slot, rec);
        this.chosen.add(slot);
      }
    }
    if (this.hasContiguousLog(msg.commitIndex)) {
      this.advanceTo(msg.commitIndex);
    } else {
      this.send({ type: 'sync-request', from: this.id, to: msg.from });
    }
  }

  private onSyncRequest(msg: { from: string }): void {
    this.send({
      type: 'sync-response',
      from: this.id,
      to: msg.from,
      records: this.chosenRecords(),
      commitIndex: this.commitIndex,
    });
  }

  private onSyncResponse(msg: { records: [number, SlotRecord][]; commitIndex: number }): void {
    // Slots at or below the sender's commitIndex are chosen values; they
    // safely overwrite any uncommitted ("dirty") local record.
    for (const [slot, rec] of msg.records) {
      if (slot > this.commitIndex && slot <= msg.commitIndex) {
        this.log.set(slot, rec);
        this.chosen.add(slot);
      }
    }
    if (this.hasContiguousLog(msg.commitIndex)) {
      this.advanceTo(Math.min(msg.commitIndex, this.maxContiguousSlot()));
    }
  }

  private hasContiguousLog(upTo: number): boolean {
    for (let slot = this.commitIndex + 1; slot <= upTo; slot++) {
      if (!this.log.has(slot)) return false;
    }
    return true;
  }

  private maxContiguousSlot(): number {
    let slot = this.commitIndex;
    while (this.log.has(slot + 1)) slot++;
    return slot;
  }

  private advanceTo(target: number): void {
    while (this.commitIndex < target && this.log.has(this.commitIndex + 1)) {
      this.commitIndex++;
      const entry = this.log.get(this.commitIndex)!.entry;
      this.applied.push(entry);
      this.onApply?.(entry, this.commitIndex);
    }
  }

  /** True when no transient leader/campaign state is in flight (leak check). */
  get isQuiescent(): boolean {
    return this.pendingCampaigns === 0;
  }
}

/** Convenience wrapper owning the shared network and clock. */
export class Cluster {
  readonly net = new Network();
  readonly clock = new ManualClock();
  readonly nodes = new Map<string, PaxosNode>();

  constructor(ids: string[], leaseDuration = 100) {
    for (const id of ids) {
      const peers = ids.filter((p) => p !== id);
      this.nodes.set(id, new PaxosNode(id, peers, this.net, this.clock, leaseDuration));
    }
  }

  node(id: string): PaxosNode {
    const n = this.nodes.get(id);
    if (!n) throw new Error(`unknown node ${id}`);
    return n;
  }

  leader(): PaxosNode | null {
    for (const n of this.nodes.values()) if (n.isLeader) return n;
    return null;
  }

  partition(groups: string[][]): void {
    this.net.partition(groups);
  }

  heal(): void {
    this.net.healAll();
    this.net.flush();
  }
}
