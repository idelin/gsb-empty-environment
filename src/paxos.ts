/**
 * Multi-Paxos majority-consensus commit engine.
 *
 * Protocol:
 *  - Phase 1 (Prepare/Promise): a candidate picks a ballot higher than any
 *    it has seen; acceptors promise not to accept lower ballots and report
 *    their uncommitted tail so the new leader can repair holes and safely
 *    re-propose minority "dirty" values.
 *  - Phase 2 (Accept/Accepted): the leader replicates one LogEntry per slot;
 *    a slot commits once a majority accepted it.
 *  - Leader lease: while the lease is valid the leader skips Phase 1 and
 *    submits consecutive LogEntries straight into Phase 2. Every quorum
 *    acceptance renews the lease.
 *  - Hole repair: after winning Phase 1 the leader re-proposes every slot in
 *    [commitIndex+1 .. maxSlotSeen] using the highest-ballot value reported
 *    by any acceptor, filling gaps with NOOP, then commits them in order.
 *
 * GC discipline: LogEntry objects and message envelopes come from shared
 * ObjectPools; overwritten entries are mutated in place or released back.
 */

import { ObjectPool } from './pool.ts';
import { SimNet } from './simnet.ts';
import type { Msg, MsgHandler } from './simnet.ts';

export const NOOP = '<noop>';
export const SLOT_BASE = 1;

export interface LogEntry {
  slot: number;
  ballot: number;
  value: string;
  committed: boolean;
}

export function makeEntry(): LogEntry {
  return { slot: -1, ballot: 0, value: '', committed: false };
}

export function resetEntry(e: LogEntry): void {
  e.slot = -1;
  e.ballot = 0;
  e.value = '';
  e.committed = false;
}

export type Role = 'follower' | 'candidate' | 'leader';

/** Ballot = term * 64 + nodeId (cluster size must stay < 64). */
export function makeBallot(term: number, nodeId: number): number {
  return term * 64 + nodeId;
}

export function ballotTerm(ballot: number): number {
  return Math.floor(ballot / 64);
}

export interface NodeStats {
  prepareRounds: number;
  fastPathProposals: number; // committed via lease (Phase 1 bypassed)
  repairProposals: number;   // slots re-proposed during Phase 1 repair
  commits: number;
  stepDowns: number;
  safetyViolations: number;
}

export interface ProposeResult {
  ok: boolean;
  slot: number;
  reason?: string;
}

export class PaxosNode implements MsgHandler {
  role: Role = 'follower';
  leaderId = -1;

  readonly id: number;
  private readonly net: SimNet;
  private readonly clusterSize: number;
  private readonly leaseDuration: number;
  readonly entryPool: ObjectPool<LogEntry>;

  private promisedBallot = 0;
  private leaderBallot = 0;
  private leaseExpiry = 0;
  private commitIndex = 0;
  private maxSlot = 0;
  private readonly log: (LogEntry | null)[] = [];

  private promiseFrom: Set<number> = new Set();
  private promiseEntries: { slot: number; ballot: number; value: string; committed: boolean }[] = [];
  private acceptCounts: number[] = [];
  private pendingValues: (string | null)[] = [];
  private commitWaiters: ((slot: number, value: string) => void)[] = [];

  readonly stats: NodeStats = {
    prepareRounds: 0,
    fastPathProposals: 0,
    repairProposals: 0,
    commits: 0,
    stepDowns: 0,
    safetyViolations: 0,
  };

  constructor(
    id: number,
    net: SimNet,
    clusterSize: number,
    leaseDuration: number,
    entryPool: ObjectPool<LogEntry>,
  ) {
    this.id = id;
    this.net = net;
    this.clusterSize = clusterSize;
    this.leaseDuration = leaseDuration;
    this.entryPool = entryPool;
    net.register(id, this);
  }

  private get quorum(): number {
    return Math.floor(this.clusterSize / 2) + 1;
  }

  private get now(): number {
    return this.net.now;
  }

  get committedIndex(): number {
    return this.commitIndex;
  }

  get currentBallot(): number {
    return this.promisedBallot;
  }

  hasValidLease(): boolean {
    return this.role === 'leader' && this.now < this.leaseExpiry;
  }

  /** Read-only view of the committed prefix [1..commitIndex]. */
  committedLog(): string[] {
    const out: string[] = [];
    for (let s = SLOT_BASE; s <= this.commitIndex; s++) {
      const e = this.log[s];
      out.push(e === null || e === undefined ? NOOP : e.value);
    }
    return out;
  }

  entryAt(slot: number): LogEntry | null {
    const e = this.log[slot];
    return e === undefined ? null : e;
  }

  // ---------------------------------------------------------------- Phase 1

  /** Start a Prepare round with a ballot strictly higher than any seen. */
  becomeCandidate(): void {
    const term = ballotTerm(this.promisedBallot) + 1;
    const ballot = makeBallot(term, this.id);
    this.role = 'candidate';
    this.leaderBallot = ballot;
    this.promiseFrom.clear();
    this.promiseEntries.length = 0;
    this.stats.prepareRounds++;
    for (let p = 0; p < this.clusterSize; p++) {
      const m = this.net.msgPool.acquire();
      m.type = 'prepare';
      m.from = this.id;
      m.to = p;
      m.ballot = ballot;
      m.firstUncommitted = this.commitIndex + 1;
      this.net.send(m);
    }
  }

  private onPrepare(m: Msg): void {
    const reply = this.net.msgPool.acquire();
    reply.type = 'promise';
    reply.from = this.id;
    reply.to = m.from;
    reply.ballot = m.ballot;
    reply.firstUncommitted = this.commitIndex + 1;
    if (m.ballot > this.promisedBallot) {
      if (this.role === 'leader') this.stepDown();
      this.promisedBallot = m.ballot;
      reply.ok = true;
      reply.entries = this.snapshotTail(m.firstUncommitted);
    } else {
      reply.ok = false;
      reply.ballot = this.promisedBallot;
    }
    this.net.send(reply);
  }

  private snapshotTail(fromSlot: number): { slot: number; ballot: number; value: string; committed: boolean }[] {
    const out: { slot: number; ballot: number; value: string; committed: boolean }[] = [];
    for (let s = fromSlot; s <= this.maxSlot; s++) {
      const e = this.log[s];
      if (e !== null && e !== undefined) {
        out.push({ slot: e.slot, ballot: e.ballot, value: e.value, committed: e.committed });
      }
    }
    return out;
  }

  private onPromise(m: Msg): void {
    if (!m.ok) {
      if (m.ballot > this.promisedBallot) {
        this.promisedBallot = m.ballot;
        this.role = 'follower';
      }
      return;
    }
    if (this.role !== 'candidate' || m.ballot !== this.leaderBallot) return;
    // NOTE: message envelopes are recycled by the transport as soon as the
    // handler returns — extract everything we need synchronously.
    if (this.promiseFrom.has(m.from)) return;
    this.promiseFrom.add(m.from);
    if (m.entries !== null) {
      for (const e of m.entries) this.promiseEntries.push(e);
    }
    if (this.promiseFrom.size >= this.quorum) {
      this.finishPhase1();
    }
  }

  /**
   * Quorum of promises reached: repair every slot in
   * [commitIndex+1 .. maxSlotSeen] with the highest-ballot reported value
   * (NOOP for genuine holes), then become an active leader with a lease.
   */
  private finishPhase1(): void {
    let maxSeen = this.maxSlot;
    for (const e of this.promiseEntries) {
      if (e.slot > maxSeen) maxSeen = e.slot;
    }
    const repairFrom = this.commitIndex + 1;
    for (let s = repairFrom; s <= maxSeen; s++) {
      let bestBallot = -1;
      let bestValue = NOOP;
      let committedValue: string | null = null;
      const self = this.log[s];
      if (self !== null && self !== undefined) {
        bestBallot = self.ballot;
        bestValue = self.value;
        if (self.committed) committedValue = self.value;
      }
      for (const e of this.promiseEntries) {
        if (e.slot !== s) continue;
        if (e.committed) committedValue = e.value;
        if (e.ballot > bestBallot) {
          bestBallot = e.ballot;
          bestValue = e.value;
        }
      }
      if (committedValue !== null) {
        // Slot was already chosen by an earlier quorum: adopt it locally.
        this.installCommitted(s, committedValue, bestBallot);
      } else {
        this.stats.repairProposals++;
        this.startAccept(s, bestValue);
      }
    }
    this.promiseFrom.clear();
    this.promiseEntries.length = 0;
    this.role = 'leader';
    this.leaderId = this.id;
    this.leaseExpiry = this.now + this.leaseDuration;
  }

  // ---------------------------------------------------------------- Phase 2

  /**
   * Client proposal. With a valid lease this bypasses Phase 1 entirely and
   * goes straight to Accept — the consecutive-entry fast path.
   */
  propose(value: string, onCommit?: (slot: number, v: string) => void): ProposeResult {
    if (this.role !== 'leader') {
      return { ok: false, slot: -1, reason: 'not-leader' };
    }
    if (!this.hasValidLease()) {
      return { ok: false, slot: -1, reason: 'lease-expired' };
    }
    const slot = this.maxSlot + 1;
    if (onCommit !== undefined) this.commitWaiters[slot] = onCommit;
    this.stats.fastPathProposals++;
    this.startAccept(slot, value);
    return { ok: true, slot };
  }

  private startAccept(slot: number, value: string): void {
    this.pendingValues[slot] = value;
    this.acceptCounts[slot] = 0;
    if (slot > this.maxSlot) this.maxSlot = slot;
    for (let p = 0; p < this.clusterSize; p++) {
      const m = this.net.msgPool.acquire();
      m.type = 'accept';
      m.from = this.id;
      m.to = p;
      m.ballot = this.leaderBallot;
      m.slot = slot;
      m.value = value;
      this.net.send(m);
    }
  }

  private onAccept(m: Msg): void {
    const reply = this.net.msgPool.acquire();
    reply.type = 'accepted';
    reply.from = this.id;
    reply.to = m.from;
    reply.slot = m.slot;
    reply.firstUncommitted = this.commitIndex + 1;
    if (m.ballot >= this.promisedBallot) {
      this.promisedBallot = m.ballot;
      reply.ballot = m.ballot;
      reply.ok = true;
      const existing = this.log[m.slot];
      if (existing !== null && existing !== undefined) {
        if (existing.committed) {
          if (existing.value !== m.value) this.stats.safetyViolations++;
        } else {
          // Mutate in place — zero-allocation overwrite of a dirty entry.
          existing.ballot = m.ballot;
          existing.value = m.value;
        }
      } else {
        const e = this.entryPool.acquire();
        e.slot = m.slot;
        e.ballot = m.ballot;
        e.value = m.value;
        e.committed = false;
        this.log[m.slot] = e;
        if (m.slot > this.maxSlot) this.maxSlot = m.slot;
      }
    } else {
      reply.ballot = this.promisedBallot;
      reply.ok = false;
    }
    this.net.send(reply);
  }

  private onAccepted(m: Msg): void {
    if (this.role !== 'leader' || m.ballot !== this.leaderBallot) return;
    if (!m.ok) return; // a higher ballot exists; step-down arrives via prepare
    // Lagging follower catch-up: its firstUncommitted is behind our commit
    // index, so push the committed prefix it is missing (also overwrites any
    // dirty minority entries it may hold in that range).
    if (m.firstUncommitted > 0 && m.firstUncommitted <= this.commitIndex) {
      for (let s = m.firstUncommitted; s <= this.commitIndex; s++) {
        const e = this.log[s];
        if (e === null || e === undefined || !e.committed) continue;
        const c = this.net.msgPool.acquire();
        c.type = 'commit';
        c.from = this.id;
        c.to = m.from;
        c.ballot = e.ballot;
        c.slot = s;
        c.value = e.value;
        this.net.send(c);
      }
    }
    const slot = m.slot;
    if (slot <= this.commitIndex) return;
    this.acceptCounts[slot] = (this.acceptCounts[slot] | 0) + 1;
    if (this.acceptCounts[slot] === this.quorum) {
      const value = this.pendingValues[slot] as string;
      this.pendingValues[slot] = null;
      this.installCommitted(slot, value, this.leaderBallot);
      this.leaseExpiry = this.now + this.leaseDuration; // lease renewal
      for (let p = 0; p < this.clusterSize; p++) {
        if (p === this.id) continue;
        const c = this.net.msgPool.acquire();
        c.type = 'commit';
        c.from = this.id;
        c.to = p;
        c.ballot = this.leaderBallot;
        c.slot = slot;
        c.value = value;
        this.net.send(c);
      }
    }
  }

  private onCommit(m: Msg): void {
    const existing = this.log[m.slot];
    if (existing !== null && existing !== undefined && existing.committed) {
      if (existing.value !== m.value) this.stats.safetyViolations++;
      return;
    }
    this.installCommitted(m.slot, m.value, m.ballot);
  }

  /** Mark a slot committed locally, advancing the contiguous commit index. */
  private installCommitted(slot: number, value: string, ballot: number): void {
    let e = this.log[slot];
    if (e === null || e === undefined) {
      e = this.entryPool.acquire();
      e.slot = slot;
      this.log[slot] = e;
      if (slot > this.maxSlot) this.maxSlot = slot;
    } else if (e.committed) {
      if (e.value !== value) this.stats.safetyViolations++;
      return;
    }
    e.ballot = ballot;
    e.value = value;
    e.committed = true;
    this.stats.commits++;
    while (this.commitIndex + 1 <= this.maxSlot) {
      const n = this.log[this.commitIndex + 1];
      if (n === null || n === undefined || !n.committed) break;
      this.commitIndex++;
    }
    const waiter = this.commitWaiters[slot];
    if (waiter !== undefined) {
      this.commitWaiters[slot] = undefined as unknown as (s: number, v: string) => void;
      waiter(slot, value);
    }
  }

  // ---------------------------------------------------------------- common

  private stepDown(): void {
    if (this.role === 'leader') this.stats.stepDowns++;
    this.role = 'follower';
    this.leaseExpiry = 0;
  }

  handle(m: Msg): void {
    // Any higher-ballot traffic forces a stale leader down.
    if (m.ballot > this.promisedBallot && this.role === 'leader' && m.type !== 'accepted') {
      this.stepDown();
    }
    switch (m.type) {
      case 'prepare': this.onPrepare(m); break;
      case 'promise': this.onPromise(m); break;
      case 'accept': this.onAccept(m); break;
      case 'accepted': this.onAccepted(m); break;
      case 'commit': this.onCommit(m); break;
    }
  }

  /** Release every pooled log entry back to the pool (test teardown). */
  dispose(): void {
    for (let s = SLOT_BASE; s <= this.maxSlot; s++) {
      const e = this.log[s];
      if (e !== null && e !== undefined) {
        this.entryPool.release(e);
        this.log[s] = null;
      }
    }
    this.maxSlot = 0;
    this.commitIndex = 0;
  }
}
