import {
  ZERO_BALLOT,
  ballotEq,
  ballotGt,
  compareBallot,
  type Ballot,
  type Command,
  type LogEntry,
} from './types.ts';
import type { Message, Network } from './network.ts';

export type Role = 'follower' | 'candidate' | 'leader';

export interface PaxosOptions {
  id: number;
  peers: number[];
  network: Network;
  leaseMs: number;
  electionTimeoutMs: number;
  now: () => number;
}

interface PromiseData {
  entries: Array<[number, LogEntry]>;
  commitIndex: number;
}

/**
 * Multi-Paxos node.
 *
 * - Phase 1 (Prepare/Promise) runs only to acquire / re-acquire leadership.
 * - A leader holding a valid lease proposes consecutive slots directly with
 *   Phase 2 (Accept/Accepted), no per-entry Prepare round.
 * - On leadership change the new leader merges the logs reported by a
 *   majority and re-proposes every uncommitted slot (highest-ballot value
 *   wins, missing slots become no-ops), which repairs holes and safely
 *   overwrites dirty minority entries once a majority accepts the repair.
 * - Commits advance only over a contiguous prefix: no-gap commit.
 */
export class PaxosNode {
  readonly id: number;
  private readonly peers: number[];
  private readonly net: Network;
  private readonly leaseMs: number;
  private readonly electionTimeoutMs: number;
  private readonly now: () => number;

  // Persistent state.
  private promised: Ballot = ZERO_BALLOT;
  readonly log = new Map<number, LogEntry>();
  commitIndex = 0;

  // Volatile state.
  role: Role = 'follower';
  leaderId: number | null = null;
  private lastHeard = 0;
  private lastCampaign = Number.NEGATIVE_INFINITY;
  private leaseUntil = Number.NEGATIVE_INFINITY; // our own leadership lease
  private leaderLeaseUntil = Number.NEGATIVE_INFINITY; // lease granted to the current leader
  private nextHeartbeatAt = 0;

  // Candidate state.
  private campaignBallot: Ballot | null = null;
  private promises = new Map<number, PromiseData>();

  // Leader state.
  private leaderBallot: Ballot | null = null;
  private nextSlot = 1;
  private acceptances = new Map<number, Set<number>>();

  /** Entries applied in commit order; a contiguous, gap-free prefix. */
  readonly applied: LogEntry[] = [];

  private readonly commitListeners = new Set<(index: number, entry: LogEntry) => void>();
  private readonly roleListeners = new Set<(role: Role) => void>();

  // Reentrancy trampoline: with synchronous delivery a commit broadcast could
  // fire in the middle of an accept broadcast. Commit messages are deferred
  // until the outermost call unwinds.
  private depth = 0;
  private pendingCommitBroadcast = false;

  constructor(opts: PaxosOptions) {
    this.id = opts.id;
    this.peers = opts.peers;
    this.net = opts.network;
    this.leaseMs = opts.leaseMs;
    this.electionTimeoutMs = opts.electionTimeoutMs;
    this.now = opts.now;
  }

  onCommit(fn: (index: number, entry: LogEntry) => void): () => void {
    this.commitListeners.add(fn);
    return () => this.commitListeners.delete(fn);
  }

  onRoleChange(fn: (role: Role) => void): () => void {
    this.roleListeners.add(fn);
    return () => this.roleListeners.delete(fn);
  }

  private majority(): number {
    return Math.floor((this.peers.length + 1) / 2) + 1;
  }

  private enter(): void {
    this.depth++;
  }

  private exit(): void {
    if (--this.depth === 0 && this.pendingCommitBroadcast) {
      this.pendingCommitBroadcast = false;
      this.broadcastCommitNow();
    }
  }

  private maxLogSlot(): number {
    let max = 0;
    for (const slot of this.log.keys()) if (slot > max) max = slot;
    return max;
  }

  // ---------------------------------------------------------------- election

  /** Phase 1: Prepare. */
  campaign(): void {
    this.enter();
    try {
    const ballot: Ballot = { round: this.promised.round + 1, nodeId: this.id };
    this.promised = ballot;
    this.setRole('candidate');
    this.campaignBallot = ballot;
    this.lastCampaign = this.now();
    this.promises.clear();
    // Promise to self.
    this.promises.set(this.id, {
      entries: this.entriesFrom(this.commitIndex + 1),
      commitIndex: this.commitIndex,
    });
    for (const peer of this.peers) {
      this.net.send({
        type: 'prepare',
        from: this.id,
        to: peer,
        ballot,
        fromIndex: this.commitIndex + 1,
      });
    }
    this.maybeBecomeLeader();
    } finally {
      this.exit();
    }
  }

  private entriesFrom(fromIndex: number): Array<[number, LogEntry]> {
    const out: Array<[number, LogEntry]> = [];
    for (const [slot, entry] of this.log) {
      if (slot >= fromIndex) out.push([slot, entry]);
    }
    out.sort((a, b) => a[0] - b[0]);
    return out;
  }

  private maybeBecomeLeader(): void {
    if (this.role !== 'candidate' || !this.campaignBallot) return;
    if (this.promises.size < this.majority()) return;
    this.becomeLeader();
  }

  /** Phase 1 won: merge majority logs, repair uncommitted slots. */
  private becomeLeader(): void {
    const ballot = this.campaignBallot;
    if (!ballot) return;
    this.leaderBallot = ballot;
    this.campaignBallot = null;
    this.leaderId = this.id;
    this.setRole('leader');
    this.leaseUntil = this.now() + this.leaseMs;
    this.nextHeartbeatAt = this.now() + this.leaseMs / 2;
    this.acceptances.clear();

    // Merge everything the majority reported; highest ballot wins per slot.
    let maxSlot = this.maxLogSlot();
    let maxCommit = this.commitIndex;
    for (const data of this.promises.values()) {
      if (data.commitIndex > maxCommit) maxCommit = data.commitIndex;
      for (const [slot, entry] of data.entries) {
        if (slot > maxSlot) maxSlot = slot;
        const cur = this.log.get(slot);
        if (!cur || ballotGt(entry.ballot, cur.ballot)) this.log.set(slot, entry);
      }
    }
    this.promises.clear();

    // Adopt anything a previous ballot already committed.
    this.advanceCommitLocal(maxCommit);

    // Hole repair: re-propose every uncommitted slot seen by the quorum.
    // Slots nobody holds become no-ops, safely overwriting minority dirt.
    for (let slot = this.commitIndex + 1; slot <= maxSlot; slot++) {
      const existing = this.log.get(slot);
      this.proposeAt(slot, existing ? existing.value : { kind: 'noop' });
    }
    this.nextSlot = maxSlot + 1;
    this.broadcastCommit();
  }

  // ------------------------------------------------------------- proposing

  /** Phase 2 fast path: only valid while the leadership lease holds. */
  propose(value: Command): number {
    this.enter();
    try {
    if (this.role !== 'leader') throw new Error(`node ${this.id}: not leader`);
    if (this.now() >= this.leaseUntil) throw new Error(`node ${this.id}: lease expired`);
    const slot = this.nextSlot++;
    this.proposeAt(slot, value);
    return slot;
    } finally {
      this.exit();
    }
  }

  private proposeAt(slot: number, value: Command): void {
    const ballot = this.leaderBallot;
    if (!ballot) throw new Error(`node ${this.id}: not leader`);
    const entry: LogEntry = { ballot, value };
    this.log.set(slot, entry);
    this.acceptances.set(slot, new Set([this.id]));
    for (const peer of this.peers) {
      this.net.send({
        type: 'accept',
        from: this.id,
        to: peer,
        ballot,
        index: slot,
        entry,
        commitIndex: this.commitIndex,
        leaderLeaseUntil: this.leaseUntil,
      });
    }
    this.tryAdvanceCommit();
  }

  private tryAdvanceCommit(): void {
    let advanced = false;
    for (;;) {
      const next = this.commitIndex + 1;
      const acceptors = this.acceptances.get(next);
      if (!acceptors || acceptors.size < this.majority() || !this.log.has(next)) break;
      this.commitIndex = next;
      this.apply(next);
      this.leaseUntil = this.now() + this.leaseMs;
      advanced = true;
    }
    if (advanced) this.broadcastCommit();
  }

  private broadcastCommit(): void {
    if (this.depth > 0) {
      this.pendingCommitBroadcast = true;
      return;
    }
    this.broadcastCommitNow();
  }

  private broadcastCommitNow(): void {
    if (!this.leaderBallot) return;
    for (const peer of this.peers) {
      this.net.send({
        type: 'commit',
        from: this.id,
        to: peer,
        ballot: this.leaderBallot,
        commitIndex: this.commitIndex,
        leaderLeaseUntil: this.leaseUntil,
      });
    }
  }

  // -------------------------------------------------------------- messages

  handle(msg: Message): void {
    this.enter();
    try {
    switch (msg.type) {
      case 'prepare':
        this.onPrepare(msg);
        break;
      case 'promise':
        this.onPromise(msg);
        break;
      case 'reject':
        this.onReject(msg);
        break;
      case 'accept':
        this.onAccept(msg);
        break;
      case 'accepted':
        this.onAccepted(msg);
        break;
      case 'commit':
        this.onCommitMsg(msg);
        break;
      case 'catchup':
        this.onCatchup(msg);
        break;
    }
    } finally {
      this.exit();
    }
  }

  private onPrepare(msg: Extract<Message, { type: 'prepare' }>): void {
    const leaseBlocks =
      this.now() < this.leaderLeaseUntil && this.leaderId !== null && msg.from !== this.leaderId;
    if (compareBallot(msg.ballot, this.promised) < 0 || leaseBlocks) {
      this.net.send({
        type: 'reject',
        from: this.id,
        to: msg.from,
        ballot: msg.ballot,
        promised: this.promised,
        inReplyTo: 'prepare',
      });
      return;
    }
    if (ballotGt(msg.ballot, this.promised)) this.promised = msg.ballot;
    if (this.role !== 'follower') this.stepDown();
    this.net.send({
      type: 'promise',
      from: this.id,
      to: msg.from,
      ballot: msg.ballot,
      entries: this.entriesFrom(msg.fromIndex),
      commitIndex: this.commitIndex,
    });
  }

  private onPromise(msg: Extract<Message, { type: 'promise' }>): void {
    if (this.role !== 'candidate') return;
    if (!this.campaignBallot || !ballotEq(msg.ballot, this.campaignBallot)) return;
    this.promises.set(msg.from, { entries: msg.entries, commitIndex: msg.commitIndex });
    this.maybeBecomeLeader();
  }

  private onReject(msg: Extract<Message, { type: 'reject' }>): void {
    if (ballotGt(msg.promised, this.promised)) this.promised = msg.promised;
    if (msg.inReplyTo === 'accept' && this.role === 'leader') this.stepDown();
  }

  private onAccept(msg: Extract<Message, { type: 'accept' }>): void {
    if (compareBallot(msg.ballot, this.promised) < 0) {
      this.net.send({
        type: 'reject',
        from: this.id,
        to: msg.from,
        ballot: msg.ballot,
        promised: this.promised,
        inReplyTo: 'accept',
      });
      return;
    }
    if (ballotGt(msg.ballot, this.promised)) this.promised = msg.ballot;
    if (this.role !== 'follower') this.stepDown();
    this.leaderId = msg.from;
    this.leaderLeaseUntil = msg.leaderLeaseUntil;
    this.lastHeard = this.now();
    this.log.set(msg.index, msg.entry);
    this.net.send({ type: 'accepted', from: this.id, to: msg.from, ballot: msg.ballot, index: msg.index });
    this.advanceCommit(msg.commitIndex, msg.ballot);
  }

  private onAccepted(msg: Extract<Message, { type: 'accepted' }>): void {
    if (this.role !== 'leader' || !this.leaderBallot || !ballotEq(msg.ballot, this.leaderBallot)) return;
    this.acceptances.get(msg.index)?.add(msg.from);
    this.tryAdvanceCommit();
  }

  private onCommitMsg(msg: Extract<Message, { type: 'commit' }>): void {
    if (compareBallot(msg.ballot, this.promised) < 0) return;
    if (ballotGt(msg.ballot, this.promised)) this.promised = msg.ballot;
    if (this.role !== 'follower') this.stepDown();
    this.leaderId = msg.from;
    this.leaderLeaseUntil = msg.leaderLeaseUntil;
    this.lastHeard = this.now();
    this.advanceCommit(msg.commitIndex, msg.ballot);
  }

  private onCatchup(msg: Extract<Message, { type: 'catchup' }>): void {
    if (this.role !== 'leader' || !this.leaderBallot) return;
    for (let slot = msg.fromIndex; slot <= this.commitIndex; slot++) {
      const entry = this.log.get(slot);
      if (!entry) continue;
      // Re-stamp committed values with the current ballot: re-accepting an
      // already-committed value under a higher ballot is safe and lets the
      // lagging follower commit it under the current leader's commit horizon.
      const restamped: LogEntry = { ballot: this.leaderBallot, value: entry.value };
      this.net.send({
        type: 'accept',
        from: this.id,
        to: msg.from,
        ballot: this.leaderBallot,
        index: slot,
        entry: restamped,
        commitIndex: this.commitIndex,
        leaderLeaseUntil: this.leaseUntil,
      });
    }
    this.net.send({
      type: 'commit',
      from: this.id,
      to: msg.from,
      ballot: this.leaderBallot,
      commitIndex: this.commitIndex,
      leaderLeaseUntil: this.leaseUntil,
    });
  }

  /**
   * No-gap commit: only ever advance over a contiguous prefix, and only over
   * entries confirmed under the committing leader's ballot (minBallot). A
   * dirty minority entry with an older ballot never commits; it stalls the
   * prefix until catch-up safely overwrites it.
   */
  private advanceCommit(target: number, minBallot: Ballot): void {
    while (this.commitIndex < target) {
      const entry = this.log.get(this.commitIndex + 1);
      if (!entry || compareBallot(entry.ballot, minBallot) < 0) break;
      this.commitIndex++;
      this.apply(this.commitIndex);
    }
    if (this.commitIndex < target && this.leaderId !== null && this.leaderId !== this.id) {
      // Hole or stale entry detected: ask the leader to re-send the prefix.
      this.net.send({ type: 'catchup', from: this.id, to: this.leaderId, fromIndex: this.commitIndex + 1 });
    }
  }

  /** Leader-side commit adoption for entries a quorum already committed. */
  private advanceCommitLocal(target: number): void {
    while (this.commitIndex < target && this.log.has(this.commitIndex + 1)) {
      this.commitIndex++;
      this.apply(this.commitIndex);
    }
  }

  private apply(index: number): void {
    const entry = this.log.get(index);
    if (!entry) return;
    this.applied.push(entry);
    for (const fn of this.commitListeners) fn(index, entry);
  }

  // ----------------------------------------------------------------- timers

  checkTimeout(): void {
    this.enter();
    try {
    const t = this.now();
    if (this.role === 'leader') {
      if (t >= this.leaseUntil) {
        this.stepDown();
        return;
      }
      if (t >= this.nextHeartbeatAt) {
        this.leaseUntil = t + this.leaseMs;
        this.broadcastCommit();
        this.nextHeartbeatAt = t + this.leaseMs / 2;
      }
      return;
    }
    if (
      t - this.lastHeard >= this.electionTimeoutMs &&
      t - this.lastCampaign >= this.electionTimeoutMs
    ) {
      this.campaign();
    }
    } finally {
      this.exit();
    }
  }

  private stepDown(): void {
    this.campaignBallot = null;
    this.leaderBallot = null;
    this.acceptances.clear();
    this.setRole('follower');
  }

  private setRole(role: Role): void {
    if (this.role === role) return;
    this.role = role;
    for (const fn of this.roleListeners) fn(role);
  }
}
