import { ballotEquals, compareBallots } from './types.ts';
import type { Ballot, Command, LogEntry, Message } from './types.ts';

export type Role = 'follower' | 'candidate' | 'leader';

export interface SubmitResult {
  status: 'proposed' | 'queued' | 'not-leader';
  slot?: number;
}

export interface NodeOptions {
  id: number;
  peers: number[];
  leaseDuration: number;
  now: () => number;
  send: (msg: Message) => void;
  onCommit?: (nodeId: number, entry: LogEntry) => void;
}

const ZERO: Ballot = { term: 0, nodeId: -1 };

/**
 * A Multi-Paxos node combining acceptor, proposer and learner roles.
 *
 * Concurrency model: actor-style. `receive()` and `submit()` are fully
 * synchronous and never await; outbound messages are only enqueued into the
 * network (never delivered re-entrantly). Every state transition is therefore
 * atomic with respect to other nodes and to other messages — there are no
 * locks to deadlock and no interleavings to race. Linearizability of the
 * committed log follows from the Paxos invariants below, not from timing.
 *
 * Protocol summary:
 * - Phase 1 (Prepare/Promise): a candidate picks a ballot higher than any it
 *   has seen and asks acceptors to promise not to accept lower ballots.
 *   Promises carry every accepted entry >= fromSlot, which lets a new leader
 *   recover (re-propose) any value that may have been chosen, fill holes with
 *   noops, and safely overwrite "dirty" minority-only entries.
 * - Phase 2 (Accept/Accepted): the leader broadcasts entries; an entry is
 *   committed once a majority has accepted it *under the leader's current
 *   ballot*, and commitIndex only advances over contiguous slots (no holes).
 * - Leader lease: while the lease is valid the leader skips Phase 1 entirely
 *   and proposes back-to-back entries with its established ballot. Lease
 *   expiry (or a higher ballot observed anywhere) forces a fresh Phase 1.
 *   Safety never depends on the lease — only liveness/efficiency do.
 */
export class PaxosNode {
  readonly id: number;
  private readonly peers: number[];
  private readonly leaseDuration: number;
  private readonly now: () => number;
  private readonly emit: (msg: Message) => void;
  private readonly commitHook: ((nodeId: number, entry: LogEntry) => void) | undefined;

  private role: Role = 'follower';
  private maxTermSeen = 0;

  // ---- acceptor state ----
  /** Highest ballot promised; applies to every slot (multi-instance prepare). */
  private promisedFloor: Ballot = { ...ZERO };
  private readonly log = new Map<number, LogEntry>();
  private commitIndex = 0;

  // ---- proposer state (scoped to this.ballot) ----
  private ballot: Ballot = { ...ZERO };
  private readonly promises = new Map<number, LogEntry[]>();
  private readonly proposed = new Map<number, LogEntry>();
  private readonly acceptedBy = new Map<number, Set<number>>();
  private nextSlot = 1;
  private leaseExpiry = -1;
  private readonly pending: Command[] = [];

  // ---- state machine ----
  private readonly kv = new Map<string, string>();

  constructor(opts: NodeOptions) {
    this.id = opts.id;
    this.peers = [...opts.peers];
    this.leaseDuration = opts.leaseDuration;
    this.now = opts.now;
    this.emit = opts.send;
    this.commitHook = opts.onCommit;
  }

  // ================= public API =================

  /** Deliver one message. Runs to completion; never re-entrant. */
  receive(msg: Message): void {
    switch (msg.type) {
      case 'Prepare':
        this.onPrepare(msg);
        break;
      case 'Promise':
        this.onPromise(msg);
        break;
      case 'Accept':
        this.onAccept(msg);
        break;
      case 'Accepted':
        this.onAccepted(msg);
        break;
      case 'Rejected':
        this.onRejected(msg);
        break;
      case 'Commit':
        this.onCommit(msg);
        break;
    }
  }

  /** Start Phase 1 with a fresh, strictly higher ballot. */
  startElection(): void {
    this.maxTermSeen += 1;
    this.ballot = { term: this.maxTermSeen, nodeId: this.id };
    this.role = 'candidate';
    this.promises.clear();
    this.proposed.clear();
    this.acceptedBy.clear();
    this.leaseExpiry = -1;
    // Self-promise: our own acceptor must not accept lower ballots either.
    if (compareBallots(this.ballot, this.promisedFloor) > 0) {
      this.promisedFloor = this.ballot;
    }
    this.promises.set(this.id, this.acceptedSince(this.commitIndex + 1));
    for (const peer of this.peers) {
      if (peer === this.id) continue;
      this.emit({
        type: 'Prepare',
        from: this.id,
        to: peer,
        ballot: { ...this.ballot },
        fromSlot: this.commitIndex + 1,
      });
    }
    this.maybeBecomeLeader();
  }

  /**
   * Client entry point. With a valid leader lease this goes straight to
   * Phase 2 (Phase 1 bypass). On lease expiry the leader re-runs Phase 1 and
   * queues the command so ordering is preserved.
   */
  submit(command: Command): SubmitResult {
    if (this.role === 'leader' && this.now() < this.leaseExpiry) {
      const slot = this.nextSlot++;
      this.propose(slot, command);
      return { status: 'proposed', slot };
    }
    if (this.role === 'leader') {
      this.pending.push(command);
      this.startElection();
      return { status: 'queued' };
    }
    if (this.role === 'candidate') {
      // Election in flight: queue so client ordering is preserved.
      this.pending.push(command);
      return { status: 'queued' };
    }
    return { status: 'not-leader' };
  }

  getRole(): Role {
    return this.role;
  }

  getCommitIndex(): number {
    return this.commitIndex;
  }

  getBallot(): Ballot {
    return { ...this.ballot };
  }

  getPromisedFloor(): Ballot {
    return { ...this.promisedFloor };
  }

  getLeaseExpiry(): number {
    return this.leaseExpiry;
  }

  getEntry(slot: number): LogEntry | undefined {
    return this.log.get(slot);
  }

  getState(): ReadonlyMap<string, string> {
    return this.kv;
  }

  /** Committed prefix of the log: always hole-free by construction. */
  getCommittedLog(): LogEntry[] {
    const out: LogEntry[] = [];
    for (let slot = 1; slot <= this.commitIndex; slot++) {
      const entry = this.log.get(slot);
      if (entry) out.push(entry);
    }
    return out;
  }

  // ================= proposer =================

  private maybeBecomeLeader(): void {
    if (this.role !== 'candidate') return;
    if (this.promises.size < this.majority()) return;
    this.role = 'leader';
    this.leaseExpiry = this.now() + this.leaseDuration;
    this.recoverUncommittedSlots();
    this.flushPending();
  }

  /**
   * Phase-1 recovery: for every slot above commitIndex, adopt the value
   * accepted under the highest ballot seen in the promise quorum (this is
   * what makes committed values survive leader changes), and fill every
   * remaining hole with a noop so the log stays contiguous.
   */
  private recoverUncommittedSlots(): void {
    let maxSlot = this.commitIndex;
    const best = new Map<number, LogEntry>();
    for (const entries of this.promises.values()) {
      for (const entry of entries) {
        if (entry.slot <= this.commitIndex) continue;
        if (entry.slot > maxSlot) maxSlot = entry.slot;
        const current = best.get(entry.slot);
        if (!current || compareBallots(entry.ballot, current.ballot) > 0) {
          best.set(entry.slot, entry);
        }
      }
    }
    this.nextSlot = maxSlot + 1;
    for (let slot = this.commitIndex + 1; slot <= maxSlot; slot++) {
      const chosen = best.get(slot);
      this.propose(slot, chosen ? chosen.command : { kind: 'noop' });
    }
  }

  private flushPending(): void {
    while (this.pending.length > 0) {
      const command = this.pending.shift() as Command;
      const slot = this.nextSlot++;
      this.propose(slot, command);
    }
  }

  /** Phase 2 for one slot: self-accept, then broadcast Accept. */
  private propose(slot: number, command: Command): void {
    if (slot <= this.commitIndex) {
      throw new Error(`invariant violated: propose slot ${slot} <= commitIndex ${this.commitIndex}`);
    }
    const entry: LogEntry = { slot, ballot: { ...this.ballot }, command };
    this.proposed.set(slot, entry);
    this.acceptedBy.set(slot, new Set([this.id]));
    this.log.set(slot, entry);
    for (const peer of this.peers) {
      if (peer === this.id) continue;
      this.emit({ type: 'Accept', from: this.id, to: peer, entry });
    }
    this.tryAdvanceCommit();
  }

  /**
   * Advance commitIndex over contiguous slots only. A slot is committable
   * iff a majority accepted it under the leader's *current* ballot — this is
   * the rule that keeps commits safe across ballot changes.
   */
  private tryAdvanceCommit(): void {
    for (;;) {
      const slot = this.commitIndex + 1;
      const entry = this.proposed.get(slot);
      if (!entry || !ballotEquals(entry.ballot, this.ballot)) return;
      const acks = this.acceptedBy.get(slot);
      if (!acks || acks.size < this.majority()) return;
      this.commitIndex = slot;
      this.apply(entry);
      this.broadcastCommit();
    }
  }

  /** Learner broadcast: followers adopt committed entries and catch up. */
  private broadcastCommit(): void {
    const entries: LogEntry[] = [];
    for (let slot = 1; slot <= this.commitIndex; slot++) {
      const entry = this.log.get(slot);
      if (entry) entries.push(entry);
    }
    for (const peer of this.peers) {
      if (peer === this.id) continue;
      this.emit({
        type: 'Commit',
        from: this.id,
        to: peer,
        term: this.ballot.term,
        commitIndex: this.commitIndex,
        entries,
      });
    }
  }

  // ================= acceptor =================

  private onPrepare(msg: Extract<Message, { type: 'Prepare' }>): void {
    if (msg.ballot.term > this.maxTermSeen) {
      this.maxTermSeen = msg.ballot.term;
      this.stepDown();
    }
    if (compareBallots(msg.ballot, this.promisedFloor) <= 0) {
      this.emit({
        type: 'Rejected',
        from: this.id,
        to: msg.from,
        ballot: msg.ballot,
        promised: { ...this.promisedFloor },
      });
      return;
    }
    this.promisedFloor = msg.ballot;
    this.emit({
      type: 'Promise',
      from: this.id,
      to: msg.from,
      ballot: msg.ballot,
      accepted: this.acceptedSince(msg.fromSlot),
    });
  }

  private onAccept(msg: Extract<Message, { type: 'Accept' }>): void {
    const ballot = msg.entry.ballot;
    if (ballot.term > this.maxTermSeen) {
      this.maxTermSeen = ballot.term;
      this.stepDown();
    }
    if (compareBallots(ballot, this.promisedFloor) < 0) {
      this.emit({
        type: 'Rejected',
        from: this.id,
        to: msg.from,
        ballot,
        promised: { ...this.promisedFloor },
      });
      return;
    }
    // Never touch already-committed slots; everything else may be overwritten
    // by a higher-ballot entry (this is how dirty minority logs get repaired).
    if (msg.entry.slot > this.commitIndex) {
      this.log.set(msg.entry.slot, msg.entry);
    }
    this.emit({ type: 'Accepted', from: this.id, to: msg.from, ballot, slot: msg.entry.slot });
  }

  // ================= learner / bookkeeping =================

  private onPromise(msg: Extract<Message, { type: 'Promise' }>): void {
    if (this.role !== 'candidate' || !ballotEquals(msg.ballot, this.ballot)) return;
    this.promises.set(msg.from, msg.accepted);
    this.maybeBecomeLeader();
  }

  private onAccepted(msg: Extract<Message, { type: 'Accepted' }>): void {
    if (this.role !== 'leader' || !ballotEquals(msg.ballot, this.ballot)) return;
    const acks = this.acceptedBy.get(msg.slot);
    if (!acks) return;
    acks.add(msg.from);
    this.tryAdvanceCommit();
  }

  private onRejected(msg: Extract<Message, { type: 'Rejected' }>): void {
    if (msg.promised.term > this.maxTermSeen) {
      this.maxTermSeen = msg.promised.term;
      this.stepDown();
    }
  }

  private onCommit(msg: Extract<Message, { type: 'Commit' }>): void {
    if (msg.term > this.maxTermSeen) {
      this.maxTermSeen = msg.term;
      this.stepDown();
    }
    // A competing leader at our term (or higher) is making progress: there can
    // only be one effective leader per term, so we step down. Without this a
    // stale leader could keep proposing with an outdated nextSlot and overwrite
    // entries it just adopted as committed.
    if (this.role !== 'follower' && msg.term >= this.ballot.term) {
      this.stepDown();
    }
    if (msg.commitIndex <= this.commitIndex) return;
    // Adopt the leader's committed entries; committed values are immutable,
    // so this also safely overwrites any conflicting uncommitted entries.
    for (const entry of msg.entries) {
      if (entry.slot > this.commitIndex && entry.slot <= msg.commitIndex) {
        this.log.set(entry.slot, entry);
      }
    }
    while (this.commitIndex < msg.commitIndex) {
      const entry = this.log.get(this.commitIndex + 1);
      if (!entry) break; // defensive: never commit across a hole
      this.commitIndex++;
      this.apply(entry);
    }
    if (this.nextSlot <= this.commitIndex) {
      this.nextSlot = this.commitIndex + 1;
    }
  }

  private stepDown(): void {
    if (this.role !== 'follower') {
      this.role = 'follower';
      this.leaseExpiry = -1;
    }
  }

  private apply(entry: LogEntry): void {
    if (entry.command.kind === 'set') {
      this.kv.set(entry.command.key, entry.command.value);
    }
    this.commitHook?.(this.id, entry);
  }

  private acceptedSince(fromSlot: number): LogEntry[] {
    const out: LogEntry[] = [];
    for (const [slot, entry] of this.log) {
      if (slot >= fromSlot) out.push(entry);
    }
    out.sort((a, b) => a.slot - b.slot);
    return out;
  }

  private majority(): number {
    return Math.floor(this.peers.length / 2) + 1;
  }
}
