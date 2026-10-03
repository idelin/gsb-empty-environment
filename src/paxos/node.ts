import { EventEmitter } from 'node:events';
import type { Clock } from './clock.ts';
import { SystemClock } from './clock.ts';
import type { Network } from './network.ts';
import type { Ballot, LogEntry, Message } from './types.ts';
import { ballotEq, compareBallot, NOOP_VALUE } from './types.ts';

export type Role = 'follower' | 'candidate' | 'leader';

export interface PaxosNodeOptions {
  /** Leader lease duration in clock milliseconds. */
  leaseMs?: number;
  clock?: Clock;
}

export class NotLeaderError extends Error {
  readonly knownLeader: string | null;
  constructor(nodeId: string, knownLeader: string | null) {
    super(`node ${nodeId} is not the leader (known leader: ${knownLeader ?? 'none'})`);
    this.name = 'NotLeaderError';
    this.knownLeader = knownLeader;
  }
}

/**
 * A Multi-Paxos replica with:
 *  - classic Prepare/Promise (phase 1) and Accept/Accepted (phase 2);
 *  - a leader lease: while valid, the leader commits consecutive log entries
 *    with phase 2 only (phase 1 is skipped entirely);
 *  - hole repair: a newly elected leader re-proposes every undecided slot in
 *    [minMajorityCommitIndex + 1, maxSeenSlot], adopting the highest-ballot
 *    value seen in promises and filling gaps with no-ops, so uncommitted
 *    "dirty" entries on a minority are safely overwritten.
 *
 * Commitment is hole-free: commitIndex only advances over a contiguous prefix
 * of majority-accepted slots.
 */
export class PaxosNode extends EventEmitter {
  readonly id: string;
  private readonly members: string[];
  private readonly network: Network;
  private readonly clock: Clock;
  private readonly leaseMs: number;

  // Persistent state.
  private promised: Ballot | null = null;
  private readonly log = new Map<number, LogEntry>();
  private readonly committedSlots = new Set<number>();
  private commitIndex = 0;

  // Volatile state.
  private role: Role = 'follower';
  private highestTerm = 0;
  private currentBallot: Ballot | null = null;
  private leaseExpiry = -Infinity;
  private readonly promises = new Map<string, { entries: LogEntry[]; commitIndex: number }>();
  private readonly acceptCounts = new Map<number, Set<string>>();
  private nextSlot = 1;
  private knownLeader: string | null = null;
  private pendingClient: string[] = [];
  private disposed = false;

  constructor(id: string, members: string[], network: Network, options: PaxosNodeOptions = {}) {
    super();
    if (members.length === 0) throw new Error('cluster must have at least one member');
    if (!members.includes(id)) throw new Error(`node ${id} is not a cluster member`);
    this.id = id;
    this.members = [...members];
    this.network = network;
    this.clock = options.clock ?? new SystemClock();
    this.leaseMs = options.leaseMs ?? 1000;
    this.network.register(id, (msg) => this.receive(msg));
  }

  get majority(): number {
    return Math.floor(this.members.length / 2) + 1;
  }

  getRole(): Role {
    return this.role;
  }
  getCommitIndex(): number {
    return this.commitIndex;
  }
  getPromised(): Ballot | null {
    return this.promised;
  }
  getCurrentBallot(): Ballot | null {
    return this.currentBallot;
  }
  getKnownLeader(): string | null {
    return this.knownLeader;
  }
  get pendingClientCount(): number {
    return this.pendingClient.length;
  }
  get logSize(): number {
    return this.log.size;
  }

  isLeader(): boolean {
    return this.role === 'leader';
  }

  /** True only while this node is leader AND its lease has not expired. */
  hasValidLease(): boolean {
    return this.role === 'leader' && this.clock.now() < this.leaseExpiry;
  }

  entry(slot: number): LogEntry | undefined {
    return this.log.get(slot);
  }

  isCommitted(slot: number): boolean {
    return this.committedSlots.has(slot);
  }

  dumpLog(): LogEntry[] {
    return [...this.log.values()].sort((a, b) => a.slot - b.slot);
  }

  committedEntries(): LogEntry[] {
    return this.dumpLog().filter((e) => e.slot <= this.commitIndex);
  }

  /** Start phase 1: ask a majority to promise on a fresh, higher ballot. */
  campaign(): void {
    const term = Math.max(this.highestTerm, this.promised?.term ?? 0) + 1;
    const ballot: Ballot = { term, nodeId: this.id };
    this.role = 'candidate';
    this.currentBallot = ballot;
    this.promises.clear();
    this.promised = ballot; // vote for self
    this.collectPromise(this.id, this.snapshotEntries(), this.commitIndex);
    this.network.broadcast(this.id, (to) => ({ type: 'Prepare', from: this.id, to, ballot }));
  }

  /**
   * Client proposal. Fast path: with a valid lease the entry is appended at
   * the next consecutive slot and committed via phase 2 only. If the lease
   * has expired the leader first re-runs phase 1 to revalidate its mandate.
   */
  propose(value: string): void {
    if (this.role !== 'leader') throw new NotLeaderError(this.id, this.knownLeader);
    if (!this.hasValidLease()) {
      this.pendingClient.push(value);
      this.campaign();
      return;
    }
    this.proposeAt(this.nextSlot, value);
    this.nextSlot++;
  }

  private receive(msg: Message): void {
    if (this.disposed) return;
    switch (msg.type) {
      case 'Prepare':
        this.onPrepare(msg.from, msg.ballot);
        break;
      case 'Promise':
        this.onPromise(msg.from, msg.ballot, msg.entries, msg.commitIndex);
        break;
      case 'Accept':
        this.onAccept(msg.from, msg.ballot, msg.entry);
        break;
      case 'Accepted':
        this.onAccepted(msg.from, msg.ballot, msg.slot);
        break;
      case 'Commit':
        this.onCommit(msg.from, msg.commitIndex, msg.entries);
        break;
    }
  }

  private onPrepare(from: string, ballot: Ballot): void {
    this.highestTerm = Math.max(this.highestTerm, ballot.term);
    if (this.promised !== null && compareBallot(ballot, this.promised) <= 0) return;
    this.promised = ballot;
    if (
      this.role !== 'follower' &&
      (this.currentBallot === null || compareBallot(ballot, this.currentBallot) > 0)
    ) {
      this.stepDown();
    }
    this.network.send({
      type: 'Promise',
      from: this.id,
      to: from,
      ballot,
      entries: this.snapshotEntries(),
      commitIndex: this.commitIndex,
    });
  }

  private onPromise(from: string, ballot: Ballot, entries: LogEntry[], commitIndex: number): void {
    this.highestTerm = Math.max(this.highestTerm, ballot.term);
    if (this.role !== 'candidate' || !ballotEq(ballot, this.currentBallot)) return;
    this.collectPromise(from, entries, commitIndex);
  }

  private collectPromise(from: string, entries: LogEntry[], commitIndex: number): void {
    this.promises.set(from, { entries, commitIndex });
    if (this.role === 'candidate' && this.promises.size >= this.majority) this.becomeLeader();
  }

  private becomeLeader(): void {
    this.role = 'leader';
    this.knownLeader = this.id;
    this.leaseExpiry = this.clock.now() + this.leaseMs;
    this.acceptCounts.clear();

    // Hole repair: re-propose every slot that some majority member has not
    // committed, adopting the highest-ballot value seen; fill gaps with no-ops.
    let minCommit = this.commitIndex;
    let maxSlot = this.maxLogSlot();
    const best = new Map<number, LogEntry>();
    for (const promise of this.promises.values()) {
      minCommit = Math.min(minCommit, promise.commitIndex);
      for (const entry of promise.entries) {
        maxSlot = Math.max(maxSlot, entry.slot);
        const current = best.get(entry.slot);
        if (current === undefined || compareBallot(entry.ballot, current.ballot) > 0) {
          best.set(entry.slot, entry);
        }
      }
    }
    for (let slot = minCommit + 1; slot <= maxSlot; slot++) {
      const chosen = best.get(slot);
      this.proposeAt(slot, chosen !== undefined ? chosen.value : NOOP_VALUE);
    }
    this.nextSlot = Math.max(this.nextSlot, maxSlot + 1);

    // Drain client values queued while the lease was being revalidated.
    for (const value of this.pendingClient.splice(0)) {
      this.proposeAt(this.nextSlot, value);
      this.nextSlot++;
    }
    this.emit('leader', this.id);
  }

  private proposeAt(slot: number, value: string): void {
    if (this.role !== 'leader' || this.currentBallot === null) return;
    const entry: LogEntry = { slot, ballot: this.currentBallot, value };
    this.log.set(slot, entry);
    this.acceptCounts.set(slot, new Set([this.id]));
    const ballot = this.currentBallot;
    this.network.broadcast(this.id, (to) => ({ type: 'Accept', from: this.id, to, ballot, entry }));
    this.checkCommit(slot);
  }

  private onAccept(from: string, ballot: Ballot, entry: LogEntry): void {
    this.highestTerm = Math.max(this.highestTerm, ballot.term);
    if (this.promised !== null && compareBallot(ballot, this.promised) < 0) return; // stale ballot
    this.promised = ballot;
    this.knownLeader = from;
    if (
      this.role !== 'follower' &&
      this.currentBallot !== null &&
      compareBallot(ballot, this.currentBallot) > 0
    ) {
      this.stepDown();
    }
    const slot = entry.slot;
    if (!this.committedSlots.has(slot)) {
      // Uncommitted entries may be overwritten by a newer ballot: this is what
      // lets a new leader safely repair a minority's dirty log.
      this.log.set(slot, entry);
    }
    this.network.send({ type: 'Accepted', from: this.id, to: from, ballot, slot });
  }

  private onAccepted(from: string, ballot: Ballot, slot: number): void {
    if (this.role !== 'leader' || !ballotEq(ballot, this.currentBallot)) return;
    const acceptors = this.acceptCounts.get(slot);
    if (acceptors === undefined) return;
    // acceptCounts is cleared on every new term, so every Accepted counted
    // here belongs to the current ballot.
    acceptors.add(from);
    this.checkCommit(slot);
  }

  private checkCommit(slot: number): void {
    if (this.role !== 'leader' || this.committedSlots.has(slot)) return;
    const acceptors = this.acceptCounts.get(slot);
    if (acceptors !== undefined && acceptors.size >= this.majority) {
      this.committedSlots.add(slot);
      // Majority contact renews the leader lease.
      this.leaseExpiry = this.clock.now() + this.leaseMs;
      if (this.tryAdvanceCommitIndex()) this.broadcastCommit();
      const entry = this.log.get(slot);
      if (entry !== undefined) this.emit('commit', entry);
    }
  }

  /** Hole-free commit: advance only over a contiguous committed prefix. */
  private tryAdvanceCommitIndex(): boolean {
    let moved = false;
    while (this.committedSlots.has(this.commitIndex + 1)) {
      this.commitIndex++;
      moved = true;
    }
    return moved;
  }

  private broadcastCommit(): void {
    const entries = this.committedEntries();
    const commitIndex = this.commitIndex;
    this.network.broadcast(this.id, (to) => ({
      type: 'Commit',
      from: this.id,
      to,
      commitIndex,
      entries,
    }));
  }

  private onCommit(from: string, commitIndex: number, entries: LogEntry[]): void {
    this.knownLeader = from;
    for (const entry of entries) {
      // Committed entries are authoritative: overwrite any divergent
      // uncommitted ("dirty") local entry at the same slot.
      this.log.set(entry.slot, entry);
      if (!this.committedSlots.has(entry.slot)) {
        this.committedSlots.add(entry.slot);
        this.emit('commit', entry);
      }
    }
    if (commitIndex > this.commitIndex) this.commitIndex = commitIndex;
  }

  private snapshotEntries(): LogEntry[] {
    return this.dumpLog();
  }

  private maxLogSlot(): number {
    let max = 0;
    for (const slot of this.log.keys()) if (slot > max) max = slot;
    return max;
  }

  private stepDown(): void {
    this.role = 'follower';
    this.currentBallot = null;
    this.leaseExpiry = -Infinity;
    this.promises.clear();
    this.acceptCounts.clear();
  }

  dispose(): void {
    this.disposed = true;
    this.network.unregister(this.id);
    this.removeAllListeners();
    this.log.clear();
    this.committedSlots.clear();
    this.promises.clear();
    this.acceptCounts.clear();
    this.pendingClient = [];
    this.commitIndex = 0;
    this.stepDown();
  }
}
