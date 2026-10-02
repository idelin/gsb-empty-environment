import { compareBallot, ZERO_BALLOT } from "./ballot.ts";
import type { Ballot } from "./ballot.ts";
import { NOOP } from "./messages.ts";
import type {
  AcceptMsg,
  CommitMsg,
  LogEntry,
  Message,
  PrepareMsg,
  PromiseMsg,
  RequestSyncMsg,
  SyncMsg,
} from "./messages.ts";

export type Role = "follower" | "candidate" | "leader";

export interface NodeOptions {
  leaseDuration: number;
  now?: () => number;
}

/**
 * Multi-Paxos node.
 *
 * Phase 1 (Prepare/Promise) is run once per election; a leader holding a
 * valid lease appends consecutive log entries with Phase 2 (Accept/Accepted)
 * only. A new leader repairs every slot above the commit index before
 * serving writes: slots seen in promises are re-proposed with the value
 * accepted at the highest ballot, unseen slots are filled with NOOP, so the
 * committed log never contains holes.
 */
export class PaxosNode {
  readonly id: string;
  private readonly peers: string[];
  private readonly majority: number;
  private readonly leaseDuration: number;
  private readonly now: () => number;

  outbox: (msg: Message) => void = () => {};

  role: Role = "follower";
  promised: Ballot = ZERO_BALLOT;
  readonly log = new Map<number, LogEntry>();
  commitIndex = 0;

  private ballot: Ballot = ZERO_BALLOT;
  private leaseExpiry = Number.NEGATIVE_INFINITY;
  private promises = new Map<string, PromiseMsg>();
  private accepted = new Map<number, Set<string>>();
  private nextSlot = 1;

  constructor(id: string, peers: string[], opts: NodeOptions) {
    this.id = id;
    this.peers = peers;
    this.majority = Math.floor((peers.length + 1) / 2) + 1;
    this.leaseDuration = opts.leaseDuration;
    this.now = opts.now ?? (() => 0);
  }

  get currentBallot(): Ballot {
    return this.ballot;
  }

  get maxSlot(): number {
    return this.nextSlot - 1;
  }

  get pendingAccepts(): number {
    return this.accepted.size;
  }

  get leaseValid(): boolean {
    return this.role === "leader" && this.now() < this.leaseExpiry;
  }

  private emit(msg: Message): void {
    this.outbox(msg);
  }

  startElection(): void {
    this.role = "candidate";
    this.ballot = { round: this.promised.round + 1, leaderId: this.id };
    this.promised = this.ballot;
    this.promises.clear();
    this.accepted.clear();
    this.promises.set(this.id, {
      type: "Promise",
      from: this.id,
      to: this.id,
      ballot: this.ballot,
      entries: this.tailEntries(this.commitIndex + 1),
      commitIndex: this.commitIndex,
    });
    for (const peer of this.peers) {
      this.emit({
        type: "Prepare",
        from: this.id,
        to: peer,
        ballot: this.ballot,
        fromSlot: this.commitIndex + 1,
      });
    }
  }

  /**
   * Propose a value as leader. Returns the assigned slot, or -1 when the
   * lease expired (a fresh election is started in that case).
   */
  propose(value: string): number {
    if (this.role !== "leader") {
      throw new Error(`${this.id} is not the leader`);
    }
    if (this.now() >= this.leaseExpiry) {
      this.startElection();
      return -1;
    }
    const slot = this.nextSlot++;
    this.acceptAt(slot, value);
    return slot;
  }

  heartbeat(): void {
    if (this.role !== "leader") return;
    for (const peer of this.peers) {
      this.emit({
        type: "Commit",
        from: this.id,
        to: peer,
        ballot: this.ballot,
        commitIndex: this.commitIndex,
      });
    }
  }

  receive(msg: Message): void {
    switch (msg.type) {
      case "Prepare":
        this.onPrepare(msg);
        break;
      case "Promise":
        this.onPromise(msg);
        break;
      case "Accept":
        this.onAccept(msg);
        break;
      case "Accepted":
        this.onAccepted(msg.from, msg.slot, msg.ballot);
        break;
      case "Commit":
        this.onCommit(msg);
        break;
      case "RequestSync":
        this.onRequestSync(msg);
        break;
      case "Sync":
        this.onSync(msg);
        break;
    }
  }

  private tailEntries(fromSlot: number): LogEntry[] {
    const entries: LogEntry[] = [];
    for (const [slot, entry] of this.log) {
      if (slot >= fromSlot) entries.push(entry);
    }
    entries.sort((a, b) => a.slot - b.slot);
    return entries;
  }

  private onPrepare(msg: PrepareMsg): void {
    if (compareBallot(msg.ballot, this.promised) <= 0) return;
    this.promised = msg.ballot;
    this.stepDown();
    this.emit({
      type: "Promise",
      from: this.id,
      to: msg.from,
      ballot: msg.ballot,
      entries: this.tailEntries(msg.fromSlot),
      commitIndex: this.commitIndex,
    });
  }

  private onPromise(msg: PromiseMsg): void {
    if (this.role !== "candidate") return;
    if (compareBallot(msg.ballot, this.ballot) !== 0) return;
    this.promises.set(msg.from, msg);
    if (this.promises.size >= this.majority) this.becomeLeader();
  }

  private becomeLeader(): void {
    this.role = "leader";
    // Phase-1 repair: merge every entry reported by the promising majority.
    const chosen = new Map<number, LogEntry>();
    let maxSlot = this.commitIndex;
    for (const promise of this.promises.values()) {
      for (const entry of promise.entries) {
        const current = chosen.get(entry.slot);
        if (!current || compareBallot(entry.ballot, current.ballot) > 0) {
          chosen.set(entry.slot, entry);
        }
        if (entry.slot > maxSlot) maxSlot = entry.slot;
      }
    }
    this.promises.clear();
    // Rewrite every slot above the commit index at the new ballot so the
    // log is contiguous: known values are re-proposed, holes become NOOP.
    for (let slot = this.commitIndex + 1; slot <= maxSlot; slot++) {
      this.acceptAt(slot, chosen.get(slot)?.value ?? NOOP);
    }
    this.nextSlot = maxSlot + 1;
    this.leaseExpiry = this.now() + this.leaseDuration;
  }

  private acceptAt(slot: number, value: string): void {
    const entry: LogEntry = { slot, ballot: this.ballot, value };
    this.log.set(slot, entry);
    this.trackAccept(slot, this.id);
    for (const peer of this.peers) {
      this.emit({
        type: "Accept",
        from: this.id,
        to: peer,
        slot,
        ballot: this.ballot,
        value,
        commitIndex: this.commitIndex,
      });
    }
  }

  private onAccept(msg: AcceptMsg): void {
    if (compareBallot(msg.ballot, this.promised) < 0) return;
    this.adopt(msg.ballot);
    this.log.set(msg.slot, { slot: msg.slot, ballot: msg.ballot, value: msg.value });
    if (msg.slot >= this.nextSlot) this.nextSlot = msg.slot + 1;
    this.emit({
      type: "Accepted",
      from: this.id,
      to: msg.from,
      slot: msg.slot,
      ballot: msg.ballot,
    });
    this.learnCommit(msg.commitIndex);
    this.maybeRequestSync(msg.from, msg.commitIndex);
  }

  private onAccepted(from: string, slot: number, ballot: Ballot): void {
    if (this.role !== "leader") return;
    if (compareBallot(ballot, this.ballot) !== 0) return;
    if (slot <= this.commitIndex) return;
    this.trackAccept(slot, from);
    this.maybeCommit();
  }

  private onCommit(msg: CommitMsg): void {
    this.adopt(msg.ballot);
    this.learnCommit(msg.commitIndex);
    this.maybeRequestSync(msg.from, msg.commitIndex);
  }

  private onRequestSync(msg: RequestSyncMsg): void {
    if (this.role !== "leader") return;
    this.emit({
      type: "Sync",
      from: this.id,
      to: msg.from,
      ballot: this.ballot,
      entries: this.tailEntries(msg.fromSlot),
      commitIndex: this.commitIndex,
    });
  }

  private onSync(msg: SyncMsg): void {
    this.adopt(msg.ballot);
    for (const entry of msg.entries) {
      const current = this.log.get(entry.slot);
      if (!current || compareBallot(entry.ballot, current.ballot) >= 0) {
        this.log.set(entry.slot, entry);
      }
      if (entry.slot >= this.nextSlot) this.nextSlot = entry.slot + 1;
    }
    this.learnCommit(msg.commitIndex);
    this.maybeRequestSync(msg.from, msg.commitIndex);
  }

  /**
   * Adopt a higher ballot observed from the current leader: step down and
   * drop every uncommitted entry accepted at an older ballot. Such entries
   * can never be chosen (a majority has promised the higher ballot), and
   * any value that was already chosen survives in the new leader's log and
   * is re-fetched via Sync.
   */
  private adopt(ballot: Ballot): void {
    if (compareBallot(ballot, this.promised) < 0) return;
    if (compareBallot(ballot, this.promised) > 0) {
      this.promised = ballot;
      this.stepDown();
    }
    for (const [slot, entry] of [...this.log]) {
      if (slot > this.commitIndex && compareBallot(entry.ballot, ballot) < 0) {
        this.log.delete(slot);
      }
    }
    let max = this.commitIndex;
    for (const slot of this.log.keys()) {
      if (slot > max) max = slot;
    }
    this.nextSlot = max + 1;
  }

  private stepDown(): void {
    this.role = "follower";
    this.promises.clear();
    this.accepted.clear();
  }

  private trackAccept(slot: number, nodeId: string): void {
    let set = this.accepted.get(slot);
    if (!set) {
      set = new Set();
      this.accepted.set(slot, set);
    }
    set.add(nodeId);
  }

  private maybeCommit(): void {
    let advanced = false;
    for (;;) {
      const next = this.commitIndex + 1;
      const entry = this.log.get(next);
      if (!entry) break;
      // Only entries written at this leader's own ballot may be committed
      // by counting acceptances; older entries commit transitively because
      // the repair phase rewrote the whole tail at the current ballot.
      if (compareBallot(entry.ballot, this.ballot) !== 0) break;
      const set = this.accepted.get(next);
      if (!set || set.size < this.majority) break;
      this.commitIndex = next;
      advanced = true;
    }
    if (advanced) {
      for (const slot of [...this.accepted.keys()]) {
        if (slot <= this.commitIndex) this.accepted.delete(slot);
      }
      for (const peer of this.peers) {
        this.emit({
          type: "Commit",
          from: this.id,
          to: peer,
          ballot: this.ballot,
          commitIndex: this.commitIndex,
        });
      }
    }
  }

  private highestContiguous(): number {
    let slot = this.commitIndex;
    while (this.log.has(slot + 1)) slot++;
    return slot;
  }

  private learnCommit(commitIndex: number): void {
    const target = Math.min(commitIndex, this.highestContiguous());
    if (target > this.commitIndex) this.commitIndex = target;
  }

  private maybeRequestSync(leaderId: string, knownCommit: number): void {
    const contiguous = this.highestContiguous();
    if (knownCommit > contiguous || this.maxSlot > contiguous) {
      this.emit({
        type: "RequestSync",
        from: this.id,
        to: leaderId,
        fromSlot: contiguous + 1,
      });
    }
  }
}
