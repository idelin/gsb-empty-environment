import { compareBallot, NOOP_VALUE } from "./types.ts";
import type { Ballot, LogEntry } from "./types.ts";
import { LogStore } from "./log-store.ts";
import { SimulatedNetwork } from "./network.ts";
import type { Message } from "./network.ts";

/**
 * Multi-Paxos node with leader-lease fast path.
 *
 * Protocol summary:
 *  - Phase 1 (Prepare/Promise): a candidate picks a ballot higher than any
 *    it has seen. Acceptors grant a promise — plus a time-boxed LEASE —
 *    and report every accepted-but-uncommitted entry so the new leader can
 *    repair holes with the already-chosen values.
 *  - Phase 2 (Accept/Accepted): while holding a valid lease the leader
 *    skips Phase 1 entirely and streams Accept for consecutive slots.
 *  - Safety: acceptors reject Prepare while an active lease is held by
 *    another ballot, and reject Accept carrying a ballot lower than the
 *    promised one. A partitioned old leader therefore cannot commit once
 *    the majority has moved on; its dirty minority entries are safely
 *    overwritten by higher-ballot Accepts after healing.
 */

export const Role = {
  Follower: 0,
  Candidate: 1,
  Leader: 2,
} as const;
export type Role = (typeof Role)[keyof typeof Role];

interface PreparePayload { ballot: Ballot }
interface PromiseEntry { slot: number; round: number; node: number; value: string }
interface PromisePayload {
  ballot: Ballot;
  ok: boolean;
  accepted: PromiseEntry[];
  leaseUntil: number;
  promisedRound: number;
}
interface AcceptPayload { slot: number; ballot: Ballot; value: string }
interface AcceptedPayload { slot: number; ballot: Ballot; ok: boolean }
interface CommitPayload { commitIndex: number }

export interface PaxosNodeOptions {
  id: number;
  allIds: number[];
  store: LogStore;
  net: SimulatedNetwork;
  now: () => number;
  leaseMs: number;
  onApply?: (slot: number, value: string) => void;
}

export class PaxosNode {
  readonly id: number;
  private readonly allIds: number[];
  private readonly majority: number;
  readonly store: LogStore;
  private readonly net: SimulatedNetwork;
  private readonly now: () => number;
  private readonly leaseMs: number;
  private readonly onApply: ((slot: number, value: string) => void) | null;

  role: Role = Role.Follower;

  // Acceptor state (durable in a real system).
  private promised: Ballot | null = null;
  private promisedLeaseUntil = 0;
  private lastRound = 0;

  // Leader/candidate state.
  private leaderBallot: Ballot | null = null;
  private leaderLeaseUntil = 0;
  private prepBallot: Ballot | null = null;
  private prepDeadline = 0;
  private promises = new Map<number, PromisePayload>();
  private promiseNacks = 0;
  private pendingAccepts = new Map<number, Set<number>>();
  private nextSlot = 0;
  private clientQueue: string[] = [];

  // Applied (committed & delivered to the state machine) values, in order.
  private readonly appliedLog: string[] = [];

  constructor(opts: PaxosNodeOptions) {
    this.id = opts.id;
    this.allIds = opts.allIds;
    this.majority = Math.floor(opts.allIds.length / 2) + 1;
    this.store = opts.store;
    this.net = opts.net;
    this.now = opts.now;
    this.leaseMs = opts.leaseMs;
    this.onApply = opts.onApply ?? null;
    this.net.register(this.id, (msg) => this.handle(msg));
  }

  // ---------------------------------------------------------------- client

  /** Client entry point. Fast path: leader with valid lease → Phase 2 only. */
  propose(value: string): void {
    if (this.role === Role.Leader && this.now() < this.leaderLeaseUntil) {
      this.fastPathAccept(value);
      return;
    }
    this.clientQueue.push(value);
    if (this.role === Role.Leader) this.stepDown(); // lease lost
    if (this.role !== Role.Candidate) this.becomeLeader();
  }

  /** Begin Phase 1 with a fresh, monotonically increasing ballot. */
  becomeLeader(): void {
    this.lastRound++;
    const ballot: Ballot = { round: this.lastRound, nodeId: this.id };
    this.role = Role.Candidate;
    this.prepBallot = ballot;
    this.prepDeadline = this.now() + this.leaseMs;
    this.promises.clear();
    this.promiseNacks = 0;
    this.net.broadcast("prepare", this.id, this.allIds, { ballot } satisfies PreparePayload);
  }

  /** Advance timers: lease expiry and election timeout. */
  tick(): void {
    if (this.role === Role.Leader && this.now() >= this.leaderLeaseUntil) {
      this.stepDown();
    }
    if (this.role === Role.Candidate && this.now() >= this.prepDeadline) {
      this.role = Role.Follower;
      this.prepBallot = null;
    }
  }

  get commitIndex(): number {
    return this.store.commitIndex;
  }

  get isLeader(): boolean {
    return this.role === Role.Leader;
  }

  appliedValues(): readonly string[] {
    return this.appliedLog;
  }

  entry(slot: number): LogEntry | undefined {
    return this.store.get(slot);
  }

  /** Re-send the full log tail + commit point. Brings healed/straggler peers up to date. */
  syncFollowers(): void {
    if (this.role !== Role.Leader) return;
    if (this.leaderBallot === null) return;
    const max = this.store.maxAcceptedSlot();
    for (let slot = 0; slot <= max; slot++) {
      const e = this.store.get(slot);
      if (e === undefined) continue;
      // Re-send with the CURRENT leader ballot: the new leader owns these
      // entries now. Re-sending the historical ballot would let a healed
      // minority node treat the stale ballot as a lease renewal and reject
      // the new leader's higher-ballot Accepts.
      this.net.broadcast("accept", this.id, this.allIds, {
        slot,
        ballot: this.leaderBallot,
        value: e.value,
      } satisfies AcceptPayload);
    }
    this.net.broadcast("commit", this.id, this.allIds, {
      commitIndex: this.store.commitIndex,
    } satisfies CommitPayload);
  }

  // ------------------------------------------------------------- messages

  private handle(msg: Message): void {
    switch (msg.type) {
      case "prepare":
        this.handlePrepare(msg.from, msg.payload as PreparePayload);
        break;
      case "promise":
        this.handlePromise(msg.from, msg.payload as PromisePayload);
        break;
      case "accept":
        this.handleAccept(msg.from, msg.payload as AcceptPayload);
        break;
      case "accepted":
        this.handleAccepted(msg.from, msg.payload as AcceptedPayload);
        break;
      case "commit":
        this.handleCommit(msg.from, msg.payload as CommitPayload);
        break;
      case "sync-request":
        this.syncFollowers();
        break;
      default:
        throw new Error(`unknown message type: ${msg.type}`);
    }
  }

  private handlePrepare(from: number, p: PreparePayload): void {
    if (p.ballot.round > this.lastRound) this.lastRound = p.ballot.round;

    const leaseActive = this.now() < this.promisedLeaseUntil;
    const higher =
      this.promised === null || compareBallot(p.ballot, this.promised) > 0;

    if (leaseActive || !higher) {
      this.net.send({
        type: "promise",
        from: this.id,
        to: from,
        payload: {
          ballot: p.ballot,
          ok: false,
          accepted: [],
          leaseUntil: this.promisedLeaseUntil,
          promisedRound: this.promised?.round ?? 0,
        } satisfies PromisePayload,
      });
      return;
    }

    this.promised = p.ballot;
    this.promisedLeaseUntil = this.now() + this.leaseMs;
    // Demote ourselves only when the winning ballot is not our own.
    const ownBallot = this.role === Role.Leader ? this.leaderBallot : this.prepBallot;
    const isOwn = ownBallot !== null && compareBallot(p.ballot, ownBallot) === 0;
    if (this.role !== Role.Follower && !isOwn) this.stepDown();

    const accepted: PromiseEntry[] = this.store.uncommittedAccepted().map((e) => ({
      slot: e.slot,
      round: e.ballotRound,
      node: e.ballotNode,
      value: e.value,
    }));
    this.net.send({
      type: "promise",
      from: this.id,
      to: from,
      payload: {
        ballot: p.ballot,
        ok: true,
        accepted,
        leaseUntil: this.promisedLeaseUntil,
        promisedRound: p.ballot.round,
      } satisfies PromisePayload,
    });
  }

  private handlePromise(from: number, p: PromisePayload): void {
    if (this.role !== Role.Candidate || this.prepBallot === null) return;
    if (compareBallot(p.ballot, this.prepBallot) !== 0) return; // stale round

    if (!p.ok) {
      this.promiseNacks++;
      if (p.promisedRound > this.lastRound) this.lastRound = p.promisedRound;
      if (this.promiseNacks >= this.majority) {
        this.role = Role.Follower; // retry on next tick/propose
        this.prepBallot = null;
      }
      return;
    }

    this.promises.set(from, p);
    if (this.promises.size < this.majority) return;

    // Majority promised → become leader, lease valid until the earliest grant.
    this.role = Role.Leader;
    this.leaderBallot = this.prepBallot;
    this.prepBallot = null;
    let leaseUntil = Number.POSITIVE_INFINITY;
    for (const pr of this.promises.values()) {
      if (pr.leaseUntil < leaseUntil) leaseUntil = pr.leaseUntil;
    }
    this.leaderLeaseUntil = leaseUntil;

    // ---- Hole repair: choose the highest-ballot accepted value per slot.
    const chosen = new Map<number, PromiseEntry>();
    let maxSlot = this.store.commitIndex;
    for (const pr of this.promises.values()) {
      for (const e of pr.accepted) {
        const prev = chosen.get(e.slot);
        if (
          prev === undefined ||
          e.round > prev.round ||
          (e.round === prev.round && e.node > prev.node)
        ) {
          chosen.set(e.slot, e);
        }
        if (e.slot > maxSlot) maxSlot = e.slot;
      }
    }
    const localMax = this.store.maxAcceptedSlot();
    if (localMax > maxSlot) maxSlot = localMax;

    for (let slot = this.store.commitIndex + 1; slot <= maxSlot; slot++) {
      const value = chosen.get(slot)?.value ?? NOOP_VALUE; // fill true holes
      this.sendAccept(slot, value);
    }
    this.nextSlot = maxSlot + 1;
    this.promises.clear();

    // Drain queued client requests through the fast path.
    const queued = this.clientQueue;
    this.clientQueue = [];
    for (const value of queued) this.fastPathAccept(value);
  }

  private handleAccept(from: number, p: AcceptPayload): void {
    if (p.ballot.round > this.lastRound) this.lastRound = p.ballot.round;

    const leaseActive = this.now() < this.promisedLeaseUntil;
    const belowPromised =
      this.promised !== null && compareBallot(p.ballot, this.promised) < 0;
    const foreignDuringLease =
      leaseActive &&
      this.promised !== null &&
      compareBallot(p.ballot, this.promised) !== 0;

    if (belowPromised || foreignDuringLease) {
      this.net.send({
        type: "accepted",
        from: this.id,
        to: from,
        payload: { slot: p.slot, ballot: p.ballot, ok: false } satisfies AcceptedPayload,
      });
      return;
    }

    const myBallot = this.role === Role.Leader ? this.leaderBallot : this.prepBallot;
    if (this.role !== Role.Follower && myBallot !== null &&
        compareBallot(p.ballot, myBallot) > 0) {
      this.stepDown(); // a newer leader exists
    }
    this.promised = p.ballot;
    this.promisedLeaseUntil = this.now() + this.leaseMs; // heartbeat renewal
    this.store.accept(p.slot, p.ballot, p.value);
    this.net.send({
      type: "accepted",
      from: this.id,
      to: from,
      payload: { slot: p.slot, ballot: p.ballot, ok: true } satisfies AcceptedPayload,
    });
  }

  private handleAccepted(from: number, p: AcceptedPayload): void {
    if (this.role !== Role.Leader || this.leaderBallot === null) return;
    if (!p.ok || compareBallot(p.ballot, this.leaderBallot) !== 0) return;

    let votes = this.pendingAccepts.get(p.slot);
    if (votes === undefined) {
      votes = new Set<number>();
      this.pendingAccepts.set(p.slot, votes);
    }
    votes.add(from);
    this.tryAdvanceCommit();
  }

  private syncRequestedFor = -1;

  private handleCommit(from: number, p: CommitPayload): void {
    if (p.commitIndex > this.store.commitIndex) {
      this.store.commitUpTo(p.commitIndex, (e) => this.applyEntry(e));
      if (this.store.commitIndex < p.commitIndex && p.commitIndex > this.syncRequestedFor) {
        // Gap in our log (missed Accepts): ask the committer to re-sync us.
        // Requested at most once per commitIndex — a lease-blocked Accept
        // will succeed after expiry instead of looping forever.
        this.syncRequestedFor = p.commitIndex;
        this.net.send({ type: "sync-request", from: this.id, to: from, payload: {} });
      }
    }
  }

  // --------------------------------------------------------------- helpers

  private fastPathAccept(value: string): void {
    const slot = this.nextSlot++;
    this.sendAccept(slot, value);
  }

  private sendAccept(slot: number, value: string): void {
    if (this.leaderBallot === null) throw new Error("sendAccept requires leadership");
    this.net.broadcast("accept", this.id, this.allIds, {
      slot,
      ballot: this.leaderBallot,
      value,
    } satisfies AcceptPayload);
  }

  private tryAdvanceCommit(): void {
    let idx = this.store.commitIndex;
    while ((this.pendingAccepts.get(idx + 1)?.size ?? 0) >= this.majority) idx++;
    if (idx > this.store.commitIndex) {
      this.store.commitUpTo(idx, (e) => this.applyEntry(e));
      this.net.broadcast("commit", this.id, this.allIds, {
        commitIndex: idx,
      } satisfies CommitPayload);
    }
  }

  private applyEntry(e: LogEntry): void {
    if (e.slot !== this.appliedLog.length) {
      throw new Error(`apply out of order: slot ${e.slot}, applied ${this.appliedLog.length}`);
    }
    this.appliedLog.push(e.value);
    if (this.onApply !== null) this.onApply(e.slot, e.value);
  }

  private stepDown(): void {
    this.role = Role.Follower;
    this.leaderBallot = null;
    this.leaderLeaseUntil = 0;
    this.prepBallot = null;
    this.pendingAccepts.clear();
  }
}
