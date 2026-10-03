import { LockFreeQueue } from '../lockfree-queue.ts';
import { NOOP, ballotKey, compareBallot } from './types.ts';
import type { Ballot, LogEntry, Message } from './types.ts';
import type { Network } from './network.ts';

export const LEASE_MS = 1_000;
const ZERO_BALLOT: Ballot = { n: 0, node: -1 };

interface PromiseData {
  accepted: Array<[number, LogEntry]>;
  commitUpTo: number;
}

/**
 * A Multi-Paxos node.
 *
 *  - Phase 1 (Prepare/Promise) elects a leader for a ballot and recovers the
 *    log: holes are filled with no-ops and the highest-ballot value wins per
 *    slot, so a new majority safely overwrites dirty minority entries.
 *  - Phase 2 (Accept/Accepted) commits entries once a majority persists them.
 *  - While the leader lease is valid, Phase 1 is skipped entirely and new
 *    entries go straight to Phase 2.
 *  - Committed entries are applied strictly in slot order — no gaps. Commit
 *    notifications carry the chosen values, so a follower can never apply a
 *    dirty (never-chosen) local entry.
 *
 * Inbound messages are pushed onto a bounded lock-free MPMC queue (shared
 * with the rest of the system) instead of being guarded by a mutex.
 */
export class PaxosNode {
  readonly id: number;
  readonly clusterSize: number;
  readonly majority: number;
  readonly leaseMs: number;

  network!: Network;
  clock: () => number;

  // --- persistent protocol state -------------------------------------
  promised: Ballot = { ...ZERO_BALLOT };
  lastBallotN = 0;
  readonly acceptedLog = new Map<number, LogEntry>();
  private readonly committed = new Set<number>();
  commitUpTo = 0;
  readonly applied: Array<{ slot: number; value: unknown }> = [];

  // --- leader state ----------------------------------------------------
  isLeader = false;
  leaderBallot: Ballot | null = null;
  private prepared = false;
  leaseExpiry = 0;
  private nextSlot = 1;
  private readonly promises = new Map<string, Map<number, PromiseData>>();
  private readonly acceptAcks = new Map<number, Map<number, string>>();
  readonly pendingValues: unknown[] = [];

  // --- lock-free inbox ---------------------------------------------------
  readonly inbox: LockFreeQueue;
  private readonly pool: Message[] = [];
  private handledCount = 0;

  constructor(
    id: number,
    clusterSize: number,
    clock: () => number,
    opts: { leaseMs?: number; inboxCapacity?: number } = {},
  ) {
    this.id = id;
    this.clusterSize = clusterSize;
    this.majority = Math.floor(clusterSize / 2) + 1;
    this.clock = clock;
    this.leaseMs = opts.leaseMs ?? LEASE_MS;
    this.inbox = new LockFreeQueue(opts.inboxCapacity ?? 1024);
  }

  // ------------------------------------------------------------------ //
  // Client API                                                          //
  // ------------------------------------------------------------------ //

  /** Propose a client value. Uses the lease fast path when possible. */
  propose(value: unknown): void {
    if (this.isLeader && this.prepared && this.clock() < this.leaseExpiry) {
      // Lease fast path: skip Phase 1, go straight to Accept.
      this.acceptPhase(this.nextSlot++, { ballot: this.leaderBallot!, value });
      return;
    }
    this.pendingValues.push(value);
    this.campaign();
  }

  /** Phase 1: ask the cluster to promise not to accept older ballots. */
  campaign(): void {
    if (this.isLeader && this.prepared && this.clock() < this.leaseExpiry) return;
    const ballot: Ballot = { n: this.lastBallotN + 1, node: this.id };
    this.noteBallot(ballot);
    this.leaderBallot = ballot;
    this.prepared = false;
    this.promised = ballot; // promise to ourselves
    this.recordPromise(this.id, ballot, {
      accepted: [...this.acceptedLog.entries()],
      commitUpTo: this.commitUpTo,
    });
    this.network.broadcast(this.id, { type: 'prepare', from: this.id, ballot });
    this.maybeFinishPhase1(ballot);
  }

  /** Leader-only: re-broadcast every accepted entry (retransmission). */
  replicate(): void {
    if (!this.isLeader || this.leaderBallot === null) return;
    for (const [slot, entry] of [...this.acceptedLog.entries()].sort((a, b) => a[0] - b[0])) {
      this.network.broadcast(this.id, { type: 'accept', from: this.id, slot, entry });
    }
    this.broadcastCommit();
  }

  // ------------------------------------------------------------------ //
  // Transport glue (lock-free inbox)                                    //
  // ------------------------------------------------------------------ //

  /** Called by the network; lock-free enqueue, never blocks. */
  receive(msg: Message): void {
    this.pool.push(msg);
    if (!this.inbox.enqueue(this.pool.length - 1)) {
      throw new Error(`node ${this.id}: inbox capacity exceeded`);
    }
  }

  /** Drain the inbox. Returns the number of messages handled. */
  processInbox(): number {
    let processed = 0;
    for (;;) {
      const idx = this.inbox.dequeue();
      if (idx === null) break;
      this.handle(this.pool[idx]);
      this.handledCount++;
      processed++;
    }
    return processed;
  }

  /** Leak check: every received message was handled, inbox is empty. */
  get inboxLeakFree(): boolean {
    return this.inbox.isEmpty() && this.handledCount === this.pool.length;
  }

  // ------------------------------------------------------------------ //
  // Message handlers                                                    //
  // ------------------------------------------------------------------ //

  private handle(msg: Message): void {
    switch (msg.type) {
      case 'prepare':
        this.onPrepare(msg);
        break;
      case 'promise':
        this.onPromise(msg);
        break;
      case 'prepare-rejected':
        this.onPrepareRejected(msg);
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
    }
  }

  private onPrepare(msg: { from: number; ballot: Ballot }): void {
    this.noteBallot(msg.ballot);
    if (compareBallot(msg.ballot, this.promised) > 0) {
      this.promised = msg.ballot;
      if (this.leaderBallot !== null && compareBallot(msg.ballot, this.leaderBallot) > 0) {
        this.isLeader = false; // a higher ballot exists: step down
      }
      this.network.send(this.id, msg.from, {
        type: 'promise',
        from: this.id,
        ballot: msg.ballot,
        accepted: [...this.acceptedLog.entries()],
        commitUpTo: this.commitUpTo,
      });
    } else {
      this.network.send(this.id, msg.from, {
        type: 'prepare-rejected',
        from: this.id,
        ballot: msg.ballot,
        promised: this.promised,
      });
    }
  }

  private onPromise(msg: {
    from: number;
    ballot: Ballot;
    accepted: Array<[number, LogEntry]>;
    commitUpTo: number;
  }): void {
    if (this.leaderBallot === null) return;
    if (ballotKey(msg.ballot) !== ballotKey(this.leaderBallot)) return;
    for (const [, entry] of msg.accepted) this.noteBallot(entry.ballot);
    this.recordPromise(msg.from, msg.ballot, {
      accepted: msg.accepted,
      commitUpTo: msg.commitUpTo,
    });
    this.maybeFinishPhase1(this.leaderBallot);
  }

  private onPrepareRejected(msg: { ballot: Ballot; promised: Ballot }): void {
    this.noteBallot(msg.promised);
    if (
      this.leaderBallot !== null &&
      ballotKey(msg.ballot) === ballotKey(this.leaderBallot) &&
      this.pendingValues.length > 0
    ) {
      this.campaign(); // retry with a higher round
    }
  }

  private onAccept(msg: { from: number; slot: number; entry: LogEntry }): void {
    this.noteBallot(msg.entry.ballot);
    if (compareBallot(msg.entry.ballot, this.promised) < 0) return; // stale ballot
    this.promised = msg.entry.ballot;
    if (
      this.leaderBallot !== null &&
      compareBallot(msg.entry.ballot, this.leaderBallot) > 0
    ) {
      this.isLeader = false;
    }
    // Overwrite any dirty (lower-ballot, uncommitted) entry for this slot.
    this.acceptedLog.set(msg.slot, msg.entry);
    this.network.send(this.id, msg.from, {
      type: 'accepted',
      from: this.id,
      slot: msg.slot,
      ballot: msg.entry.ballot,
    });
  }

  private onAccepted(msg: { from: number; slot: number; ballot: Ballot }): void {
    let acks = this.acceptAcks.get(msg.slot);
    if (acks === undefined) {
      acks = new Map();
      this.acceptAcks.set(msg.slot, acks);
    }
    acks.set(msg.from, ballotKey(msg.ballot));
    const entry = this.acceptedLog.get(msg.slot);
    if (entry === undefined) return;
    const wanted = ballotKey(entry.ballot);
    let count = 0;
    for (const key of acks.values()) if (key === wanted) count++;
    if (count >= this.majority) this.markCommitted(msg.slot);
  }

  private onCommit(msg: {
    ballot: Ballot;
    commitUpTo: number;
    entries: Array<[number, LogEntry]>;
  }): void {
    this.noteBallot(msg.ballot);
    // Install the chosen values: committed entries are final and safely
    // replace any dirty local entry for the same slot.
    for (const [slot, entry] of msg.entries) {
      if (slot > msg.commitUpTo) continue;
      const local = this.acceptedLog.get(slot);
      if (local === undefined || ballotKey(local.ballot) !== ballotKey(entry.ballot)) {
        this.acceptedLog.set(slot, entry);
      }
    }
    this.advanceCommit(msg.commitUpTo);
  }

  // ------------------------------------------------------------------ //
  // Protocol internals                                                  //
  // ------------------------------------------------------------------ //

  private recordPromise(from: number, ballot: Ballot, data: PromiseData): void {
    const key = ballotKey(ballot);
    let collected = this.promises.get(key);
    if (collected === undefined) {
      collected = new Map();
      this.promises.set(key, collected);
    }
    collected.set(from, data);
  }

  /** Once a majority promised, recover the log and become leader. */
  private maybeFinishPhase1(ballot: Ballot): void {
    const key = ballotKey(ballot);
    const collected = this.promises.get(key);
    if (collected === undefined || collected.size < this.majority) return;
    if (this.prepared && this.leaderBallot !== null &&
        ballotKey(this.leaderBallot) === key) return;

    // Adopt the furthest commit point known to the majority, installing the
    // chosen values that came with the promises.
    for (const data of collected.values()) {
      for (const [slot, entry] of data.accepted) {
        if (slot <= data.commitUpTo) this.acceptedLog.set(slot, entry);
      }
      if (data.commitUpTo > this.commitUpTo) this.advanceCommit(data.commitUpTo);
    }

    // Recovery: per slot pick the highest-ballot entry; fill holes with no-ops.
    const chosen = new Map<number, LogEntry>();
    let maxSlot = this.commitUpTo;
    for (const data of collected.values()) {
      for (const [slot, entry] of data.accepted) {
        if (slot <= this.commitUpTo) continue;
        const prev = chosen.get(slot);
        if (prev === undefined || compareBallot(entry.ballot, prev.ballot) > 0) {
          chosen.set(slot, entry);
        }
        if (slot > maxSlot) maxSlot = slot;
      }
    }
    for (let slot = this.commitUpTo + 1; slot <= maxSlot; slot++) {
      const recovered = chosen.get(slot);
      const value = recovered === undefined ? NOOP : recovered.value;
      const entry: LogEntry = { ballot, value };
      this.acceptedLog.set(slot, entry);
      this.network.broadcast(this.id, { type: 'accept', from: this.id, slot, entry });
      this.selfAck(slot, ballot);
    }

    this.isLeader = true;
    this.prepared = true;
    this.leaseExpiry = this.clock() + this.leaseMs;
    this.nextSlot = maxSlot + 1;

    // Drain queued client values straight through Phase 2.
    const pending = this.pendingValues.splice(0, this.pendingValues.length);
    for (const value of pending) {
      this.acceptPhase(this.nextSlot++, { ballot, value });
    }
  }

  /** Phase 2 for a single entry (leader). */
  private acceptPhase(slot: number, entry: LogEntry): void {
    this.acceptedLog.set(slot, entry);
    this.network.broadcast(this.id, { type: 'accept', from: this.id, slot, entry });
    this.selfAck(slot, entry.ballot);
  }

  private selfAck(slot: number, ballot: Ballot): void {
    this.onAccepted({ from: this.id, slot, ballot });
  }

  private markCommitted(slot: number): void {
    if (this.committed.has(slot)) return;
    this.committed.add(slot);
    const before = this.commitUpTo;
    this.applyContiguous();
    if (this.commitUpTo > before && this.isLeader && this.leaderBallot !== null) {
      this.broadcastCommit();
    }
  }

  private broadcastCommit(): void {
    if (this.leaderBallot === null) return;
    const entries: Array<[number, LogEntry]> = [];
    for (let slot = 1; slot <= this.commitUpTo; slot++) {
      const entry = this.acceptedLog.get(slot);
      if (entry !== undefined) entries.push([slot, entry]);
    }
    this.network.broadcast(this.id, {
      type: 'commit',
      from: this.id,
      ballot: this.leaderBallot,
      commitUpTo: this.commitUpTo,
      entries,
    });
  }

  private advanceCommit(leaderCommitUpTo: number): void {
    for (let slot = this.commitUpTo + 1; slot <= leaderCommitUpTo; slot++) {
      this.committed.add(slot);
    }
    this.applyContiguous();
  }

  /** Apply committed entries strictly in slot order — never past a hole. */
  private applyContiguous(): void {
    for (;;) {
      const slot = this.commitUpTo + 1;
      if (!this.committed.has(slot)) return;
      const entry = this.acceptedLog.get(slot);
      if (entry === undefined) return; // hole: wait for retransmission
      this.commitUpTo = slot;
      this.applied.push({ slot, value: entry.value });
    }
  }

  private noteBallot(ballot: Ballot): void {
    if (ballot.n > this.lastBallotN) this.lastBallotN = ballot.n;
  }
}
