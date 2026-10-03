import { ZERO_BALLOT, compareBallot, ballotEquals, formatBallot } from './ballot.ts';
import type { Ballot } from './ballot.ts';
import { NOOP } from './types.ts';
import type {
  AcceptedSlot,
  LogEntry,
  Message,
  MessageType,
  NodeId,
  Value,
} from './types.ts';

export type Role = 'follower' | 'candidate' | 'leader';

export interface NodeOptions {
  readonly id: NodeId;
  /** All cluster members, including self. */
  readonly peers: readonly NodeId[];
  /** Ticks without leader contact before this node starts an election. */
  readonly electionTimeout: number;
  /** Leader lease length in ticks. */
  readonly leaseDuration: number;
  /** Ticks between leader heartbeats. */
  readonly heartbeatInterval: number;
}

export interface ProposeResult {
  readonly ok: boolean;
  readonly slot?: number;
  readonly reason?: string;
}

/**
 * Multi-Paxos protocol state machine for a single node.
 *
 * Determinism: the node never reads a real clock and never uses randomness.
 * Time advances only through `tick(now)`, driven by the cluster harness.
 *
 * Core invariants maintained here:
 *  - `promised` ballot is monotonically non-decreasing.
 *  - A value is accepted only at a ballot >= `promised`.
 *  - Committed slots (`applied` prefix) are never overwritten.
 *  - The committed prefix is hole-free by construction: `applied` only grows
 *    by appending the entry of the very next slot.
 */
export class PaxosNode {
  readonly id: NodeId;
  readonly peers: readonly NodeId[];
  readonly quorum: number;

  private readonly electionTimeout: number;
  private readonly leaseDuration: number;
  private readonly heartbeatInterval: number;

  role: Role = 'follower';

  /** Highest ballot this acceptor has promised not to vote below. Monotonic. */
  promised: Ballot = { ...ZERO_BALLOT };

  /** Sparse acceptor log; holes are allowed here but never in `applied`. */
  private readonly slots = new Map<number, LogEntry>();

  /** Committed, contiguous, hole-free prefix of the log. */
  readonly applied: Value[] = [];

  // ---- candidate state ----
  private candidateBallot: Ballot | null = null;
  private candidateDeadline = 0;
  private readonly promisesReceived = new Set<NodeId>();
  private readonly repairTable = new Map<number, AcceptedSlot>();

  // ---- leader state ----
  leaderBallot: Ballot | null = null;
  private nextSlot = 0;
  private leaseUntil = -1;
  private lastHeartbeat = -1;
  private readonly heartbeatAcks = new Set<NodeId>();
  private readonly acceptAcks = new Map<number, Set<NodeId>>();

  // ---- follower timing ----
  private leaderContactUntil: number;
  private leaseGrantedUntil = -1;
  private leaseGrantedTo: NodeId | null = null;

  private now = 0;
  private outbox: Message[] = [];

  readonly sentStats: Record<MessageType, number> = {
    Prepare: 0,
    Promise: 0,
    Accept: 0,
    Accepted: 0,
    Commit: 0,
    Heartbeat: 0,
    HeartbeatAck: 0,
  };

  /** Optional tracer, wired by the cluster harness / demo. */
  trace: (line: string) => void = () => {};

  constructor(opts: NodeOptions) {
    this.id = opts.id;
    this.peers = [...opts.peers].sort();
    this.quorum = Math.floor(this.peers.length / 2) + 1;
    this.electionTimeout = opts.electionTimeout;
    this.leaseDuration = opts.leaseDuration;
    this.heartbeatInterval = opts.heartbeatInterval;
    this.leaderContactUntil = opts.electionTimeout;
  }

  // ------------------------------------------------------------------
  // Read-only views used by tests and the invariant checker
  // ------------------------------------------------------------------

  get commitIndex(): number {
    return this.applied.length - 1;
  }

  get isLeader(): boolean {
    return this.role === 'leader';
  }

  get leaseActive(): boolean {
    return this.role === 'leader' && this.now < this.leaseUntil;
  }

  entryAt(slot: number): LogEntry | undefined {
    return this.slots.get(slot);
  }

  acceptedEntries(): Array<[number, LogEntry]> {
    return [...this.slots.entries()].sort((a, b) => a[0] - b[0]);
  }

  acceptedSnapshot(): AcceptedSlot[] {
    return this.acceptedEntries().map(([slot, e]) => ({
      slot,
      ballot: e.ballot,
      value: e.value,
    }));
  }

  drainOutbox(): Message[] {
    const out = this.outbox;
    this.outbox = [];
    return out;
  }

  // ------------------------------------------------------------------
  // Clock
  // ------------------------------------------------------------------

  tick(now: number): void {
    this.now = now;
    if (this.role === 'leader') {
      if (now >= this.leaseUntil) {
        this.trace(`lease expired at ballot ${formatBallot(this.leaderBallot!)}; stepping down`);
        this.stepDown();
      } else if (now >= this.lastHeartbeat + this.heartbeatInterval) {
        this.sendHeartbeat();
      }
    } else if (this.role === 'candidate') {
      if (now >= this.candidateDeadline) this.startElection();
    } else if (now >= this.leaderContactUntil && now >= this.leaseGrantedUntil) {
      this.startElection();
    }
  }

  // ------------------------------------------------------------------
  // Client API (leader only); bypasses Phase 1 while the lease holds
  // ------------------------------------------------------------------

  propose(value: Value): ProposeResult {
    if (this.role !== 'leader' || this.leaderBallot === null) {
      return { ok: false, reason: 'not-leader' };
    }
    if (this.now >= this.leaseUntil) {
      return { ok: false, reason: 'lease-expired' };
    }
    const slot = this.nextSlot++;
    this.selfAccept(slot, value);
    this.broadcastAccept(slot, value);
    return { ok: true, slot };
  }

  // ------------------------------------------------------------------
  // Phase 1: Prepare / Promise
  // ------------------------------------------------------------------

  private startElection(): void {
    const ballot: Ballot = { round: this.promised.round + 1, node: this.id };
    this.role = 'candidate';
    this.candidateBallot = ballot;
    this.promised = ballot; // promise to self
    this.promisesReceived.clear();
    this.promisesReceived.add(this.id);
    this.repairTable.clear();
    for (const s of this.acceptedSnapshot()) this.mergeRepair(s);
    this.candidateDeadline = this.now + this.electionTimeout;
    this.leaderContactUntil = this.now + this.electionTimeout;
    this.trace(`campaigning at ballot ${formatBallot(ballot)}`);
    for (const peer of this.peers) {
      if (peer !== this.id) this.send({ type: 'Prepare', from: this.id, to: peer, ballot });
    }
    if (this.promisesReceived.size >= this.quorum) this.becomeLeader();
  }

  private onPrepare(msg: Message & { type: 'Prepare' }): void {
    // Honor the lease we granted to a live leader: refuse to vote for others.
    if (this.now < this.leaseGrantedUntil && this.leaseGrantedTo !== msg.from) return;
    if (compareBallot(msg.ballot, this.promised) < 0) return; // stale ballot: ignore
    this.promised = msg.ballot;
    if (this.role !== 'follower') this.stepDown();
    this.leaderContactUntil = this.now + this.electionTimeout;
    this.send({
      type: 'Promise',
      from: this.id,
      to: msg.from,
      ballot: msg.ballot,
      accepted: this.acceptedSnapshot(),
    });
  }

  private onPromise(msg: Message & { type: 'Promise' }): void {
    if (this.role !== 'candidate' || this.candidateBallot === null) return;
    if (!ballotEquals(msg.ballot, this.candidateBallot)) return;
    this.promisesReceived.add(msg.from);
    for (const s of msg.accepted) this.mergeRepair(s);
    if (this.promisesReceived.size >= this.quorum) this.becomeLeader();
  }

  private mergeRepair(s: AcceptedSlot): void {
    const cur = this.repairTable.get(s.slot);
    if (cur === undefined || compareBallot(s.ballot, cur.ballot) > 0) {
      this.repairTable.set(s.slot, s);
    }
  }

  // ------------------------------------------------------------------
  // Phase 2: Accept / Accepted
  // ------------------------------------------------------------------

  private becomeLeader(): void {
    this.role = 'leader';
    this.leaderBallot = this.candidateBallot!;
    this.candidateBallot = null;
    this.leaseUntil = this.now + this.leaseDuration;
    this.lastHeartbeat = this.now;
    this.acceptAcks.clear();
    this.heartbeatAcks.clear();
    this.trace(`became leader at ballot ${formatBallot(this.leaderBallot)} (lease +${this.leaseDuration})`);

    // Hole repair: re-propose every slot seen in the promise quorum at our
    // ballot. Slots no acceptor reported are sealed with NOOP. This is what
    // safely overwrites minority "dirty" entries: they lose to the value
    // chosen by the intersecting quorum (or to NOOP if none was chosen).
    let maxSlot = -1;
    for (const slot of this.repairTable.keys()) maxSlot = Math.max(maxSlot, slot);
    for (let slot = 0; slot <= maxSlot; slot++) {
      const chosen = this.repairTable.get(slot);
      const value = chosen ? chosen.value : NOOP;
      this.selfAccept(slot, value);
      this.broadcastAccept(slot, value);
    }
    this.nextSlot = maxSlot + 1;
  }

  private selfAccept(slot: number, value: Value): void {
    if (slot <= this.commitIndex) return; // committed slots are immutable
    this.slots.set(slot, { ballot: this.leaderBallot!, value, acceptedAt: this.now });
    this.acceptAcks.set(slot, new Set([this.id]));
  }

  private broadcastAccept(slot: number, value: Value): void {
    for (const peer of this.peers) {
      if (peer === this.id) continue;
      this.send({
        type: 'Accept',
        from: this.id,
        to: peer,
        ballot: this.leaderBallot!,
        slot,
        value,
        commitIndex: this.commitIndex,
      });
    }
  }

  private onAccept(msg: Message & { type: 'Accept' }): void {
    if (this.now < this.leaseGrantedUntil && this.leaseGrantedTo !== msg.from) return;
    if (compareBallot(msg.ballot, this.promised) < 0) return;
    this.promised = msg.ballot;
    if (this.role !== 'follower') this.stepDown();
    this.grantLease(msg.from);
    if (msg.slot > this.commitIndex) {
      // Overwrite any older-ballot entry; a committed slot is never touched.
      this.slots.set(msg.slot, { ballot: msg.ballot, value: msg.value, acceptedAt: this.now });
    }
    this.applyCommit(msg.commitIndex, msg.ballot);
    this.send({ type: 'Accepted', from: this.id, to: msg.from, ballot: msg.ballot, slot: msg.slot });
  }

  private onAccepted(msg: Message & { type: 'Accepted' }): void {
    if (this.role !== 'leader' || this.leaderBallot === null) return;
    if (!ballotEquals(msg.ballot, this.leaderBallot)) return;
    let acks = this.acceptAcks.get(msg.slot);
    if (acks === undefined) {
      acks = new Set();
      this.acceptAcks.set(msg.slot, acks);
    }
    acks.add(msg.from);
    this.tryAdvanceCommit();
  }

  // ------------------------------------------------------------------
  // Commit propagation
  // ------------------------------------------------------------------

  /** Leader-side commit: advance the hole-free prefix over quorum-acked slots. */
  private tryAdvanceCommit(): void {
    let advanced = false;
    for (;;) {
      const slot = this.applied.length;
      const acks = this.acceptAcks.get(slot);
      if (acks === undefined || acks.size < this.quorum) break;
      const entry = this.slots.get(slot);
      if (entry === undefined) break; // cannot happen: we self-accepted it
      this.applied.push(entry.value);
      advanced = true;
    }
    if (advanced) {
      this.leaseUntil = this.now + this.leaseDuration; // quorum proved reachable
      this.trace(`commitIndex advanced to ${this.commitIndex}`);
      for (const peer of this.peers) {
        if (peer === this.id) continue;
        this.send({
          type: 'Commit',
          from: this.id,
          to: peer,
          ballot: this.leaderBallot!,
          commitIndex: this.commitIndex,
        });
      }
    }
  }

  /**
   * Follower-side commit: apply the contiguous accepted prefix only, and only
   * entries accepted at the committing leader's ballot. A local entry from an
   * older ballot may be a minority "dirty" value that lost to a different
   * chosen value, so it must wait for the leader to re-send the chosen entry
   * (heartbeat repair) instead of being applied blindly.
   */
  private applyCommit(leaderCommitIndex: number, ballot: Ballot): void {
    while (this.applied.length <= leaderCommitIndex) {
      const entry = this.slots.get(this.applied.length);
      if (entry === undefined) break; // hole: wait for leader repair
      if (!ballotEquals(entry.ballot, ballot)) break; // dirty/foreign entry: wait for chosen value
      this.applied.push(entry.value);
    }
  }

  private onCommit(msg: Message & { type: 'Commit' }): void {
    if (compareBallot(msg.ballot, this.promised) < 0) return;
    this.promised = msg.ballot;
    if (this.role !== 'follower') this.stepDown();
    this.grantLease(msg.from);
    this.applyCommit(msg.commitIndex, msg.ballot);
  }

  // ------------------------------------------------------------------
  // Lease heartbeats
  // ------------------------------------------------------------------

  private sendHeartbeat(): void {
    this.lastHeartbeat = this.now;
    this.heartbeatAcks.clear();
    this.heartbeatAcks.add(this.id);
    // Retransmit uncommitted proposals: an Accept may have been lost to a
    // transient partition, and the leader is responsible for driving every
    // proposed slot to a quorum.
    for (let slot = this.applied.length; slot < this.nextSlot; slot++) {
      const acks = this.acceptAcks.get(slot);
      if (acks !== undefined && acks.size >= this.quorum) continue;
      const entry = this.slots.get(slot);
      if (entry !== undefined && ballotEquals(entry.ballot, this.leaderBallot!)) {
        this.broadcastAccept(slot, entry.value);
      }
    }
    for (const peer of this.peers) {
      if (peer === this.id) continue;
      this.send({
        type: 'Heartbeat',
        from: this.id,
        to: peer,
        ballot: this.leaderBallot!,
        commitIndex: this.commitIndex,
      });
    }
  }

  private onHeartbeat(msg: Message & { type: 'Heartbeat' }): void {
    if (this.now < this.leaseGrantedUntil && this.leaseGrantedTo !== msg.from) return;
    if (compareBallot(msg.ballot, this.promised) < 0) return;
    this.promised = msg.ballot;
    if (this.role !== 'follower') this.stepDown();
    this.grantLease(msg.from);
    this.applyCommit(msg.commitIndex, msg.ballot);
    this.send({
      type: 'HeartbeatAck',
      from: this.id,
      to: msg.from,
      ballot: msg.ballot,
      appliedLen: this.applied.length,
    });
  }

  private onHeartbeatAck(msg: Message & { type: 'HeartbeatAck' }): void {
    if (this.role !== 'leader' || this.leaderBallot === null) return;
    if (!ballotEquals(msg.ballot, this.leaderBallot)) return;
    this.heartbeatAcks.add(msg.from);
    if (this.heartbeatAcks.size >= this.quorum) {
      this.leaseUntil = this.now + this.leaseDuration; // lease renewal requires a quorum
    }
    // Lagging-follower repair: re-send the first slot the follower is missing.
    if (msg.appliedLen <= this.commitIndex) {
      const entry = this.slots.get(msg.appliedLen);
      if (entry !== undefined) {
        this.send({
          type: 'Accept',
          from: this.id,
          to: msg.from,
          ballot: this.leaderBallot,
          slot: msg.appliedLen,
          value: entry.value,
          commitIndex: this.commitIndex,
        });
      }
    }
  }

  private grantLease(leader: NodeId): void {
    this.leaseGrantedUntil = this.now + this.leaseDuration;
    this.leaseGrantedTo = leader;
    this.leaderContactUntil = this.now + this.electionTimeout;
  }

  // ------------------------------------------------------------------
  // Plumbing
  // ------------------------------------------------------------------

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
      case 'Commit':
        this.onCommit(msg);
        break;
      case 'Heartbeat':
        this.onHeartbeat(msg);
        break;
      case 'HeartbeatAck':
        this.onHeartbeatAck(msg);
        break;
    }
  }

  private stepDown(): void {
    this.role = 'follower';
    this.leaderBallot = null;
    this.candidateBallot = null;
    this.acceptAcks.clear();
    this.heartbeatAcks.clear();
    this.leaderContactUntil = this.now + this.electionTimeout;
  }

  private send(msg: Message): void {
    this.sentStats[msg.type] += 1;
    this.outbox.push(msg);
  }
}
