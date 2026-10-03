/**
 * PaxosNode: the Multi-Paxos protocol state machine for a single node.
 *
 * Core invariants maintained here:
 *  - Promise monotonicity: `promised` never decreases; an acceptor never
 *    accepts a ballot lower than its promise.
 *  - Hole-free commit: `commitIndex` only ever covers a contiguous prefix of
 *    the log (slots 1..commitIndex all present).
 *  - Leader completeness: a new leader re-proposes every slot 1..maxSlot seen
 *    in its majority quorum of promises under its own ballot (filling holes
 *    with NOOP), choosing the value from the highest-ballot entry per slot,
 *    before serving client traffic. Committed values are therefore preserved
 *    and uncommitted "dirty" values on a minority are safely overwritten.
 *  - Leader lease: while the lease is valid the leader skips Phase 1 and
 *    appends consecutive slots directly with Phase 2.
 */
import { NOOP_VALUE, ZERO_BALLOT, ballotKey, compareBallot } from './types.ts';
import type {
  AcceptMsg,
  AcceptedMsg,
  Ballot,
  CommitMsg,
  LogEntry,
  Message,
  PrepareMsg,
  PromiseMsg,
} from './types.ts';
import type { Cluster } from './cluster.ts';

export type Role = 'follower' | 'candidate' | 'leader';

/** Leader lease duration in logical clock ticks. */
export const LEASE_DURATION = 50;

export interface ProposeResult {
  ok: boolean;
  reason?: 'not-leader';
  leaderHint?: number | null;
  slot?: number;
  deferred?: boolean;
}

export class PaxosNode {
  readonly id: number;
  private readonly cluster: Cluster;

  role: Role = 'follower';
  /** Highest ballot this node has promised to (never decreases). */
  promised: Ballot = { ...ZERO_BALLOT };
  /** Highest round this node has ever seen (drives ballot minting). */
  highestRoundSeen = 0;
  /** Accepted entries by slot (sparse: holes are allowed here, never in commit). */
readonly log: Map<number, LogEntry> = new Map();
  /** Hole-free commit frontier: slots 1..commitIndex are committed. */
  commitIndex = 0;
  knownLeader: number | null = null;
  /** Logical clock time until which this leader may skip Phase 1. */
  leaseUntil = -1;

  private myBallot: Ballot | null = null;
  private promiseCount = 0;
  private readonly promiseEntries: Array<Map<number, LogEntry>> = [];
  private readonly acceptedBy: Map<number, Set<number>> = new Map();
  private nextSlot = 1;
  private readonly pendingClientValues: string[] = [];

  constructor(id: number, cluster: Cluster) {
    this.id = id;
    this.cluster = cluster;
  }

  private majority(): number {
    return this.cluster.majority();
  }

  /** Number of client values still waiting for a (re-)election to finish. */
  get pendingClientCount(): number {
    return this.pendingClientValues.length;
  }

  // ---------------------------------------------------------------- Phase 1

  /** Start Phase 1: mint a fresh ballot and ask every peer for a promise. */
  startElection(): void {
    this.role = 'candidate';
    this.highestRoundSeen += 1;
    this.myBallot = { round: this.highestRoundSeen, nodeId: this.id };
    this.promised = this.myBallot;
    this.knownLeader = null;
    this.promiseCount = 1; // promise to self
    this.promiseEntries.length = 0;
    this.promiseEntries.push(new Map(this.log)); // own accepted entries
    for (const peer of this.cluster.peerIds(this.id)) {
      this.cluster.send({ type: 'Prepare', from: this.id, to: peer, ballot: this.myBallot });
    }
    this.maybeBecomeLeader(); // degenerate case: single-node cluster
  }

  private onPrepare(msg: PrepareMsg): void {
    if (msg.ballot.round > this.highestRoundSeen) this.highestRoundSeen = msg.ballot.round;
    if (compareBallot(msg.ballot, this.promised) > 0) {
      this.promised = { ...msg.ballot };
      this.stepDown();
      this.cluster.send({
        type: 'Promise',
        from: this.id,
        to: msg.from,
        ballot: msg.ballot,
        ok: true,
        entries: [...this.log.entries()].map(([slot, e]) => [slot, { ballot: { ...e.ballot }, value: e.value }]),
        commitIndex: this.commitIndex,
      });
    } else {
      this.cluster.send({
        type: 'Promise',
        from: this.id,
        to: msg.from,
        ballot: msg.ballot,
        ok: false,
        entries: [],
        commitIndex: this.commitIndex,
        promised: { ...this.promised },
      });
    }
  }

  private onPromise(msg: PromiseMsg): void {
    if (this.role !== 'candidate' || this.myBallot === null) return;
    if (ballotKey(msg.ballot) !== ballotKey(this.myBallot)) return; // stale reply
    if (!msg.ok) {
      if (msg.promised && compareBallot(msg.promised, this.myBallot) > 0) {
        if (msg.promised.round > this.highestRoundSeen) this.highestRoundSeen = msg.promised.round;
        this.stepDown();
      }
      return;
    }
    this.promiseCount += 1;
    this.promiseEntries.push(new Map(msg.entries));
    this.maybeBecomeLeader();
  }

  /**
   * Become leader once a majority promised. Repair the log before serving
   * client traffic: for every slot known to the quorum, re-propose the value
   * from the highest-ballot entry; fill holes with NOOP. All repair entries
   * carry the new ballot, so a quorum of accepts makes them safe to commit.
   */
  private maybeBecomeLeader(): void {
    if (this.role !== 'candidate' || this.myBallot === null) return;
    if (this.promiseCount < this.majority()) return;

    this.role = 'leader';
    this.knownLeader = this.id;
    this.leaseUntil = this.cluster.now + LEASE_DURATION;

    const chosen: Map<number, LogEntry> = new Map();
    let maxSlot = 0;
    for (const entries of this.promiseEntries) {
      for (const [slot, entry] of entries) {
        if (slot > maxSlot) maxSlot = slot;
        const cur = chosen.get(slot);
        if (!cur || compareBallot(entry.ballot, cur.ballot) > 0) chosen.set(slot, entry);
      }
    }
    this.promiseEntries.length = 0; // quorum state no longer needed: no leak

    this.acceptedBy.clear();
    for (let slot = 1; slot <= maxSlot; slot++) {
      const entry = chosen.get(slot);
      this.broadcastAccept(slot, entry ? entry.value : NOOP_VALUE);
    }
    this.nextSlot = maxSlot + 1;

    while (this.pendingClientValues.length > 0) {
      const value = this.pendingClientValues.shift()!;
      this.broadcastAccept(this.nextSlot++, value);
    }

    // Announce the inherited commit frontier so followers that missed earlier
    // Commit notifications (e.g. while partitioned) converge after repair.
    if (this.commitIndex > 0) {
      const ballot = this.myBallot;
      for (const peer of this.cluster.peerIds(this.id)) {
        this.cluster.send({ type: 'Commit', from: this.id, to: peer, ballot, commitIndex: this.commitIndex });
      }
    }
  }

  // ---------------------------------------------------------------- Phase 2

  private broadcastAccept(slot: number, value: string): void {
    const ballot = this.myBallot!;
    for (const peer of [...this.cluster.peerIds(this.id), this.id]) {
      this.cluster.send({ type: 'Accept', from: this.id, to: peer, ballot, slot, value });
    }
  }

  /**
   * Client proposal entry point. Under a valid lease the leader goes straight
   * to Phase 2 with the next consecutive slot; an expired lease forces a
   * fresh Phase 1 (the value is queued and proposed once re-elected).
   */
  propose(value: string): ProposeResult {
    if (this.role !== 'leader' || this.myBallot === null) {
      return { ok: false, reason: 'not-leader', leaderHint: this.knownLeader };
    }
    if (this.cluster.now >= this.leaseUntil) {
      this.pendingClientValues.push(value);
      this.startElection();
      return { ok: true, deferred: true };
    }
    const slot = this.nextSlot++;
    this.broadcastAccept(slot, value);
    return { ok: true, slot };
  }

  private onAccept(msg: AcceptMsg): void {
    if (msg.ballot.round > this.highestRoundSeen) this.highestRoundSeen = msg.ballot.round;
    if (compareBallot(msg.ballot, this.promised) >= 0) {
      this.promised = { ...msg.ballot };
      if (msg.from !== this.id && this.role !== 'follower') this.stepDown();
      this.knownLeader = msg.from;
      // Overwrite is safe: msg.ballot >= promised >= ballot of any entry we hold,
      // and quorum intersection protects already-committed values.
      this.log.set(msg.slot, { ballot: { ...msg.ballot }, value: msg.value });
      this.cluster.send({ type: 'Accepted', from: this.id, to: msg.from, ballot: msg.ballot, slot: msg.slot, ok: true });
    } else {
      this.cluster.send({
        type: 'Accepted',
        from: this.id,
        to: msg.from,
        ballot: msg.ballot,
        slot: msg.slot,
        ok: false,
        promised: { ...this.promised },
      });
    }
  }

  private onAccepted(msg: AcceptedMsg): void {
    if (this.role !== 'leader' || this.myBallot === null) return;
    if (ballotKey(msg.ballot) !== ballotKey(this.myBallot)) return; // stale reply
    if (!msg.ok) {
      if (msg.promised && compareBallot(msg.promised, this.myBallot) > 0) {
        if (msg.promised.round > this.highestRoundSeen) this.highestRoundSeen = msg.promised.round;
        this.stepDown();
      }
      return;
    }
    let set = this.acceptedBy.get(msg.slot);
    if (!set) {
      set = new Set<number>();
      this.acceptedBy.set(msg.slot, set);
    }
    set.add(msg.from);
    this.tryAdvanceCommit();
  }

  /**
   * Advance the commit frontier over the contiguous prefix of slots accepted
   * by a majority under the current ballot. Never skips a hole.
   */
  private tryAdvanceCommit(): void {
    let advanced = false;
    for (;;) {
      const next = this.commitIndex + 1;
      const set = this.acceptedBy.get(next);
      if (set && set.size >= this.majority()) {
        this.commitIndex = next;
        advanced = true;
      } else {
        break;
      }
    }
    if (advanced) {
      const ballot = this.myBallot!;
      for (const peer of this.cluster.peerIds(this.id)) {
        this.cluster.send({ type: 'Commit', from: this.id, to: peer, ballot, commitIndex: this.commitIndex });
      }
    }
  }

  private onCommit(msg: CommitMsg): void {
    if (msg.commitIndex > this.commitIndex) {
      // Commit only what we actually hold contiguously: never across a hole.
      this.commitIndex = Math.min(msg.commitIndex, this.contiguousPrefixLength());
    }
  }

  // ---------------------------------------------------------------- common

  private stepDown(): void {
    this.role = 'follower';
    this.myBallot = null;
    this.knownLeader = null;
    this.acceptedBy.clear();
    this.pendingClientValues.length = 0; // client must retry against the new leader
  }

  private contiguousPrefixLength(): number {
    let n = 0;
    while (this.log.has(n + 1)) n++;
    return n;
  }

  /** Committed values of the hole-free prefix, in slot order. */
  committedValues(): string[] {
    const out: string[] = [];
    for (let slot = 1; slot <= this.commitIndex; slot++) {
      out.push(this.log.get(slot)!.value);
    }
    return out;
  }

  entryAt(slot: number): LogEntry | undefined {
    return this.log.get(slot);
  }

  handle(msg: Message): void {
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
    }
  }
}
