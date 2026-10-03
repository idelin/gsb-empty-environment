import {
  GENESIS_PARENT,
  NOOP_VALUE,
  ballotGt,
  ballotGte,
  compareBallots,
} from './types.ts';
import type {
  Ballot,
  ClusterEvent,
  LogEntry,
  Message,
  NodeId,
  Role,
  SlotRecord,
} from './types.ts';
import type { Network } from './network.ts';

export interface NodeOptions {
  leaseDuration: number;
  majority: number;
  peers: NodeId[];
  heartbeatInterval?: number;
}

/**
 * A Multi-Paxos node.
 *
 * Phase 1 (Prepare/Promise) elects a leader and repairs the uncommitted
 * suffix: the new leader adopts, for every open slot, the value accepted at
 * the highest ballot seen by the promising majority, and fills holes with
 * no-ops. Phase 2 (Accept/Accepted) re-anchors those entries under the new
 * ballot. While the leader lease is valid, client proposals skip Phase 1
 * entirely and go straight to Accept for consecutive slots; commits always
 * advance over a contiguous, gap-free prefix.
 */
export class PaxosNode {
  readonly id: NodeId;
  role: Role = 'follower';
  promised: Ballot | null = null;
  readonly log = new Map<number, SlotRecord>();
  commitIndex = -1;
  knownLeader: NodeId | null = null;
  leaseExpiry = 0;

  private readonly net: Network;
  private readonly opts: NodeOptions;
  private readonly emit: (e: ClusterEvent) => void;
  private now = 0;

  private currentBallot: Ballot | null = null;
  private highestTermSeen = 0;
  private promisesReceived = new Map<NodeId, LogEntry[]>();
  private acceptCounts = new Map<number, Set<NodeId>>();
  private pendingValues: string[] = [];
  private nextSlot = 0;
  private nextHeartbeatAt = 0;
  private electionDeadline = 0;

  constructor(
    id: NodeId,
    net: Network,
    opts: NodeOptions,
    emit: (e: ClusterEvent) => void,
  ) {
    this.id = id;
    this.net = net;
    this.opts = opts;
    this.emit = emit;
  }

  get peers(): NodeId[] {
    return this.opts.peers;
  }

  get majority(): number {
    return this.opts.majority;
  }

  get pendingClientValues(): number {
    return this.pendingValues.length;
  }

  get openAcceptRounds(): number {
    return this.acceptCounts.size;
  }

  get pendingPromises(): number {
    return this.promisesReceived.size;
  }

  get uncommittedChosen(): number {
    let n = 0;
    for (const r of this.log.values()) {
      if (r.chosen && !r.committed) n++;
    }
    return n;
  }

  onTick(now: number): void {
    this.now = now;
    if (this.role === 'leader' && now > this.leaseExpiry) {
      this.stepDown();
      return;
    }
    // Election timeout: abandon a candidacy that never reached majority.
    if (
      this.role !== 'leader' &&
      this.currentBallot !== null &&
      now > this.electionDeadline
    ) {
      this.promisesReceived.clear();
      this.currentBallot = null;
    }
    if (this.role === 'leader' && now >= this.nextHeartbeatAt) {
      this.nextHeartbeatAt = now + (this.opts.heartbeatInterval ?? 50);
      const ballot = this.currentBallot;
      if (ballot) {
        const commitIndex = this.commitIndex;
        this.broadcast(() => ({
          kind: 'heartbeat',
          from: this.id,
          ballot,
          commitIndex,
        }));
      }
    }
  }

  private hasLease(): boolean {
    return this.role === 'leader' && this.now <= this.leaseExpiry;
  }

  private broadcast(msg: (to: NodeId) => Message): void {
    for (const p of this.opts.peers) {
      if (p !== this.id) this.net.send(this.id, p, msg(p));
    }
  }

  // ---------------------------------------------------------------- Phase 1

  startElection(): void {
    if (this.role === 'leader' && this.hasLease()) return;
    this.stepDown();
    const term = this.highestTermSeen + 1;
    this.highestTermSeen = term;
    const ballot: Ballot = { term, nodeId: this.id };
    this.currentBallot = ballot;
    this.electionDeadline = this.now + this.opts.leaseDuration;
    this.promised = ballot;
    this.promisesReceived.clear();
    this.promisesReceived.set(this.id, this.acceptedEntries());
    this.broadcast(() => ({ kind: 'prepare', from: this.id, ballot }));
  }

  /** Re-broadcast prepares for the in-flight ballot (timeout retry). */
  retryElection(): void {
    if (this.role === 'leader' || !this.currentBallot) return;
    const ballot = this.currentBallot;
    this.broadcast(() => ({ kind: 'prepare', from: this.id, ballot }));
  }

  private acceptedEntries(): LogEntry[] {
    return [...this.log.values()].map((r) => r.entry);
  }

  private onPrepare(ballot: Ballot, from: NodeId): void {
    if (ballotGt(ballot, this.promised)) {
      this.promised = ballot;
      this.highestTermSeen = Math.max(this.highestTermSeen, ballot.term);
      if (this.role === 'leader') this.stepDown();
      this.knownLeader = null;
      this.net.send(this.id, from, {
        kind: 'promise',
        from: this.id,
        ballot,
        accepted: this.acceptedEntries(),
      });
    } else {
      this.net.send(this.id, from, {
        kind: 'nack',
        from: this.id,
        ballot,
        promised: this.promised,
      });
    }
  }

  private onPromise(ballot: Ballot, from: NodeId, accepted: LogEntry[]): void {
    if (this.role === 'leader') return;
    if (!this.currentBallot || compareBallots(ballot, this.currentBallot) !== 0) {
      return;
    }
    // Stale candidacy: we have since promised a higher ballot.
    if (ballotGt(this.promised, this.currentBallot)) return;
    this.promisesReceived.set(from, accepted);
    if (this.promisesReceived.size >= this.majority) {
      this.becomeLeader();
    }
  }

  private onNack(promised: Ballot | null): void {
    if (promised && ballotGt(promised, this.promised)) {
      this.promised = promised;
      this.highestTermSeen = Math.max(this.highestTermSeen, promised.term);
    }
    // A leader nacked by a higher-ballot acceptor has been superseded.
    if (
      this.role === 'leader' &&
      this.currentBallot &&
      ballotGt(promised, this.currentBallot)
    ) {
      this.stepDown();
    }
    // Back off: abandon the in-flight election so no stale state leaks.
    this.promisesReceived.clear();
    this.currentBallot = null;
  }

  private onHeartbeat(ballot: Ballot, from: NodeId, commitIndex: number): void {
    if (ballotGte(ballot, this.promised)) {
      this.promised = ballot;
      this.highestTermSeen = Math.max(this.highestTermSeen, ballot.term);
      if (ballot.nodeId !== this.id) {
        this.knownLeader = ballot.nodeId;
        if (this.role === 'leader') this.stepDown();
      }
      if (commitIndex > this.commitIndex) {
        this.net.send(this.id, from, {
          kind: 'sync-request',
          from: this.id,
          sinceCommitIndex: this.commitIndex,
        });
      }
    } else {
      this.net.send(this.id, from, {
        kind: 'nack',
        from: this.id,
        ballot,
        promised: this.promised,
      });
    }
  }

  private onSyncRequest(from: NodeId, sinceCommitIndex: number): void {
    if (this.role !== 'leader') return;
    for (let s = sinceCommitIndex + 1; s <= this.commitIndex; s++) {
      const rec = this.log.get(s);
      if (rec && rec.committed) {
        const entry = rec.entry;
        this.net.send(this.id, from, { kind: 'commit', from: this.id, entry });
      }
    }
  }

  // ---------------------------------------------------------------- leader

  private becomeLeader(): void {
    this.role = 'leader';
    this.knownLeader = this.id;
    this.leaseExpiry = this.now + this.opts.leaseDuration;
    const ballot = this.currentBallot!;
    this.emit({ type: 'leader', term: ballot.term, nodeId: this.id });

    // --- Hole repair: merge acceptor state from the promising majority.
    let maxSlot = -1;
    const adopted = new Map<number, LogEntry>();
    for (const entries of this.promisesReceived.values()) {
      for (const e of entries) {
        maxSlot = Math.max(maxSlot, e.slot);
        const cur = adopted.get(e.slot);
        if (!cur || compareBallots(e.ballot, cur.ballot) > 0) {
          adopted.set(e.slot, e);
        }
      }
    }
    for (let s = this.commitIndex + 1; s <= maxSlot; s++) {
      const found = adopted.get(s);
      const entry: LogEntry = found
        ? { ...found, ballot }
        : {
            slot: s,
            ballot,
            value: NOOP_VALUE,
            parentSlot: s === 0 ? GENESIS_PARENT : s - 1,
          };
      this.log.set(s, { entry, chosen: false, committed: false });
    }
    this.promisesReceived.clear();

    // --- Re-anchor every open slot under the new ballot (Phase 2).
    for (let s = this.commitIndex + 1; s <= maxSlot; s++) {
      this.sendAccept(s);
    }
    this.nextSlot = Math.max(maxSlot, this.commitIndex) + 1;

    // Drain queued client values under the lease (Phase 1 bypass).
    const queued = this.pendingValues;
    this.pendingValues = [];
    for (const v of queued) this.propose(v);
  }

  private sendAccept(slot: number): void {
    const rec = this.log.get(slot);
    if (!rec || rec.committed || !this.currentBallot) return;
    this.acceptCounts.set(slot, new Set([this.id]));
    const entry = rec.entry;
    this.broadcast(() => ({
      kind: 'accept',
      from: this.id,
      ballot: this.currentBallot!,
      entry,
    }));
  }

  /** Client proposal. Requires a valid leader lease; skips Phase 1. */
  propose(value: string): boolean {
    if (!this.hasLease()) {
      this.pendingValues.push(value);
      return false;
    }
    const slot = this.nextSlot++;
    const entry: LogEntry = {
      slot,
      ballot: this.currentBallot!,
      value,
      parentSlot: slot === 0 ? GENESIS_PARENT : slot - 1,
    };
    this.log.set(slot, { entry, chosen: false, committed: false });
    this.sendAccept(slot);
    return true;
  }

  private onAccepted(ballot: Ballot, from: NodeId, slot: number): void {
    if (this.role !== 'leader') return;
    if (!this.currentBallot || compareBallots(ballot, this.currentBallot) !== 0) {
      return;
    }
    const rec = this.log.get(slot);
    if (!rec || rec.chosen) return;
    let set = this.acceptCounts.get(slot);
    if (!set) {
      set = new Set();
      this.acceptCounts.set(slot, set);
    }
    set.add(from);
    if (set.size >= this.majority) {
      rec.chosen = true;
      this.acceptCounts.delete(slot);
      // Lease renewal: productive leaders keep bypassing Phase 1.
      this.leaseExpiry = this.now + this.opts.leaseDuration;
      this.advanceCommitIndex();
    }
  }

  private advanceCommitIndex(): void {
    for (;;) {
      const next = this.log.get(this.commitIndex + 1);
      if (!next || !next.chosen || next.committed) break;
      next.committed = true;
      this.commitIndex++;
      this.emit({
        type: 'commit',
        nodeId: this.id,
        slot: next.entry.slot,
        value: next.entry.value,
      });
      if (this.role === 'leader') {
        const entry = next.entry;
        this.broadcast(() => ({ kind: 'commit', from: this.id, entry }));
      }
    }
  }

  // -------------------------------------------------------------- acceptor

  private onAccept(ballot: Ballot, from: NodeId, entry: LogEntry): void {
    if (ballotGte(ballot, this.promised)) {
      this.promised = ballot;
      this.highestTermSeen = Math.max(this.highestTermSeen, ballot.term);
      if (ballot.nodeId !== this.id) this.knownLeader = ballot.nodeId;
      if (this.role === 'leader' && ballot.nodeId !== this.id) this.stepDown();
      const existing = this.log.get(entry.slot);
      if (!existing || !existing.committed) {
        // Safe overwrite: only uncommitted (dirty) records may be replaced.
        this.log.set(entry.slot, { entry, chosen: false, committed: false });
      }
      this.net.send(this.id, from, {
        kind: 'accepted',
        from: this.id,
        ballot,
        slot: entry.slot,
      });
    } else {
      this.net.send(this.id, from, {
        kind: 'nack',
        from: this.id,
        ballot,
        promised: this.promised,
      });
    }
  }

  private onCommitNotice(entry: LogEntry): void {
    if (entry.slot <= this.commitIndex) return;
    const existing = this.log.get(entry.slot);
    if (!existing || !existing.committed) {
      // Committed truth heals missing holes and dirty minority records.
      this.log.set(entry.slot, { entry, chosen: true, committed: false });
    } else {
      existing.chosen = true;
    }
    this.advanceCommitIndex();
  }

  private stepDown(): void {
    if (this.role === 'leader' && this.currentBallot) {
      this.emit({
        type: 'stepdown',
        nodeId: this.id,
        term: this.currentBallot.term,
      });
    }
    this.role = 'follower';
    this.promisesReceived.clear();
    this.acceptCounts.clear();
  }

  // ---------------------------------------------------------------- influx

  receive(msg: Message): void {
    switch (msg.kind) {
      case 'prepare':
        this.onPrepare(msg.ballot, msg.from);
        break;
      case 'promise':
        this.onPromise(msg.ballot, msg.from, msg.accepted);
        break;
      case 'nack':
        this.onNack(msg.promised);
        break;
      case 'accept':
        this.onAccept(msg.ballot, msg.from, msg.entry);
        break;
      case 'accepted':
        this.onAccepted(msg.ballot, msg.from, msg.slot);
        break;
      case 'commit':
        this.onCommitNotice(msg.entry);
        break;
      case 'heartbeat':
        this.onHeartbeat(msg.ballot, msg.from, msg.commitIndex);
        break;
      case 'sync-request':
        this.onSyncRequest(msg.from, msg.sinceCommitIndex);
        break;
    }
  }

  /** Test/inspector hook: committed values of the gap-free prefix. */
  committedPrefix(): string[] {
    const out: string[] = [];
    for (let s = 0; s <= this.commitIndex; s++) {
      out.push(this.log.get(s)!.entry.value);
    }
    return out;
  }
}
