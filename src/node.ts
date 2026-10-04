/**
 * Multi-Paxos node: majority-quorum consensus over a replicated log with
 *  - Phase 1 (Prepare/Promise) leader election + log repair (hole filling),
 *  - Phase 2 (Accept/Accepted) replication,
 *  - a leader lease that lets a stable leader skip Phase 1 and commit
 *    consecutive log entries directly,
 *  - commit notices that advance followers' commit watermark and safely
 *    overwrite dirty (never-committed) minority entries.
 *
 * Concurrency model: every state mutation happens inside a per-node Mutex
 * critical section that never awaits, so handlers are race-free and the
 * system is deadlock-free by construction (no lock is held across I/O).
 */
import { Mutex } from './mutex.ts';
import type { Network } from './network.ts';

export interface Ballot {
  round: number;
  nodeId: number;
}

export function compareBallots(a: Ballot, b: Ballot): number {
  return a.round - b.round || a.nodeId - b.nodeId;
}

function sameBallot(a: Ballot | null, b: Ballot | null): boolean {
  return !!a && !!b && a.round === b.round && a.nodeId === b.nodeId;
}

export interface Command {
  clientId: string;
  seq: number;
  data: string;
}

/** value === null is a no-op filler used to repair holes. */
export interface LogEntry {
  ballot: Ballot;
  value: Command | null;
  committed: boolean;
}

export function sameValue(a: Command | null, b: Command | null): boolean {
  if (a === null || b === null) return a === b;
  return a.clientId === b.clientId && a.seq === b.seq && a.data === b.data;
}

export type PrepareMsg = { type: 'prepare'; ballot: Ballot };
export type PrepareReply = {
  ok: boolean;
  promised: Ballot;
  log: Record<number, LogEntry>;
  committed: number;
};
export type AcceptMsg = {
  type: 'accept';
  slot: number;
  ballot: Ballot;
  value: Command | null;
};
export type AcceptReply = { ok: boolean; promised: Ballot };
export type CommitMsg = {
  type: 'commit';
  slot: number;
  entry: LogEntry;
  committed: number;
};
export type CommitReply = { ok: boolean };
export type Msg = PrepareMsg | AcceptMsg | CommitMsg;

export class NotLeaderError extends Error {
  readonly nodeId: number;

  constructor(nodeId: number) {
    super(`node ${nodeId} is not the (current) leader`);
    this.name = 'NotLeaderError';
    this.nodeId = nodeId;
  }
}

export interface NodeOptions {
  leaseMs: number;
  now?: () => number;
}

export class PaxosNode {
  readonly id: number;
  private readonly peers: number[];
  private readonly net: Network;
  private readonly leaseMs: number;
  private readonly now: () => number;

  private readonly mutex = new Mutex();
  private promised: Ballot = { round: 0, nodeId: -1 };
  private highestSeen: Ballot = { round: 0, nodeId: -1 };
  private readonly log = new Map<number, LogEntry>();
  private committed = 0; // commit watermark: slots 1..committed are committed
  private tail = 0; // highest slot ever allocated/observed
  private role: 'follower' | 'leader' = 'follower';
  private leaderBallot: Ballot | null = null;
  private leaseExpiry = 0;
  private inflightCampaign: Promise<boolean> | null = null;

  readonly stats = { prepareSent: 0, acceptSent: 0, commitSent: 0, campaigns: 0 };
  /** Protocol safety violations observed by this node (must stay empty). */
  readonly safetyViolations: string[] = [];

  constructor(id: number, peers: number[], net: Network, opts: NodeOptions) {
    this.id = id;
    this.peers = peers;
    this.net = net;
    this.leaseMs = opts.leaseMs;
    this.now = opts.now ?? Date.now;
    net.register(id, (msg) => this.dispatch(msg as Msg));
  }

  get isLeader(): boolean {
    return this.role === 'leader';
  }

  get commitIndex(): number {
    return this.committed;
  }

  get currentBallot(): Ballot {
    return this.promised;
  }

  get logSize(): number {
    return this.log.size;
  }

  private get majority(): number {
    return Math.floor(this.peers.length / 2) + 1;
  }

  /** Committed prefix of the log (slots 1..commitIndex). */
  committedValues(): Array<Command | null> {
    const out: Array<Command | null> = [];
    for (let s = 1; s <= this.committed; s++) {
      const e = this.log.get(s);
      if (!e) {
        this.safetyViolations.push(`committed slot ${s} missing from log`);
        out.push(null);
      } else {
        out.push(e.value);
      }
    }
    return out;
  }

  entryAt(slot: number): LogEntry | undefined {
    return this.log.get(slot);
  }

  // ---------------------------------------------------------------- acceptor

  private dispatch(msg: Msg): Promise<PrepareReply | AcceptReply | CommitReply> {
    return this.mutex.run(() => this.handleLocked(msg));
  }

  private handleLocked(msg: Msg): PrepareReply | AcceptReply | CommitReply {
    switch (msg.type) {
      case 'prepare':
        return this.onPrepare(msg);
      case 'accept':
        return this.onAccept(msg);
      case 'commit':
        return this.onCommit(msg);
    }
  }

  private noteBallot(b: Ballot): void {
    if (compareBallots(b, this.highestSeen) > 0) this.highestSeen = b;
  }

  private onPrepare(msg: PrepareMsg): PrepareReply {
    this.noteBallot(msg.ballot);
    if (compareBallots(msg.ballot, this.promised) > 0) {
      this.promised = msg.ballot;
      // A higher ballot exists: any lease we hold is void.
      if (this.role === 'leader' && compareBallots(msg.ballot, this.leaderBallot!) > 0) {
        this.role = 'follower';
        this.leaseExpiry = 0;
      }
    }
    const log: Record<number, LogEntry> = {};
    for (const [slot, e] of this.log) log[slot] = e;
    return {
      ok: compareBallots(this.promised, msg.ballot) === 0,
      promised: this.promised,
      log,
      committed: this.committed,
    };
  }

  private onAccept(msg: AcceptMsg): AcceptReply {
    this.noteBallot(msg.ballot);
    if (compareBallots(msg.ballot, this.promised) < 0) {
      return { ok: false, promised: this.promised };
    }
    const existing = this.log.get(msg.slot);
    if (existing && existing.committed && !sameValue(existing.value, msg.value)) {
      // Committed entries are immutable: never overwrite a decided value.
      this.safetyViolations.push(
        `slot ${msg.slot}: attempt to overwrite committed value with a different one`,
      );
      return { ok: false, promised: this.promised };
    }
    this.log.set(msg.slot, {
      ballot: msg.ballot,
      value: msg.value,
      committed: existing?.committed ?? false,
    });
    if (compareBallots(msg.ballot, this.promised) > 0) this.promised = msg.ballot;
    if (msg.slot > this.tail) this.tail = msg.slot;
    return { ok: true, promised: this.promised };
  }

  private onCommit(msg: CommitMsg): CommitReply {
    const existing = this.log.get(msg.slot);
    if (existing?.committed) {
      if (!sameValue(existing.value, msg.entry.value)) {
        this.safetyViolations.push(
          `slot ${msg.slot}: conflicting commit notice for already-committed value`,
        );
      }
      return { ok: true };
    }
    // Committed values are authoritative: overwrite any dirty minority entry.
    this.log.set(msg.slot, {
      ballot: msg.entry.ballot,
      value: msg.entry.value,
      committed: true,
    });
    if (msg.slot > this.tail) this.tail = msg.slot;
    this.advanceWatermark();
    return { ok: true };
  }

  private advanceWatermark(): void {
    for (;;) {
      const next = this.log.get(this.committed + 1);
      if (next && next.committed) this.committed++;
      else break;
    }
  }

  // ----------------------------------------------------------------- leader

  private async gather<T>(msg: Msg): Promise<Array<T | null>> {
    if (msg.type === 'prepare') this.stats.prepareSent += this.peers.length;
    if (msg.type === 'accept') this.stats.acceptSent += this.peers.length;
    const results = await Promise.all(
      this.peers.map(async (p): Promise<T | null> => {
        try {
          return await this.net.call<T>(this.id, p, msg);
        } catch {
          return null; // unreachable peer counts as no vote
        }
      }),
    );
    await this.mutex.run(() => {
      for (const r of results) {
        const promised = (r as { promised?: Ballot } | null)?.promised;
        if (promised) this.noteBallot(promised);
      }
    });
    return results;
  }

  /**
   * Phase 1: bid for leadership with a fresh ballot, then repair the log
   * (re-propose chosen values, fill holes with no-ops) before serving writes.
   * Concurrent calls are coalesced into a single election to avoid
   * self-inflicted ballot storms.
   */
  async campaign(): Promise<boolean> {
    const existing = this.inflightCampaign;
    if (existing) return existing;
    const p = this.doCampaign();
    this.inflightCampaign = p;
    try {
      return await p;
    } finally {
      if (this.inflightCampaign === p) this.inflightCampaign = null;
    }
  }

  private async doCampaign(): Promise<boolean> {
    this.stats.campaigns++;
    const ballot = await this.mutex.run(() => {
      const b: Ballot = { round: this.highestSeen.round + 1, nodeId: this.id };
      this.noteBallot(b);
      return b;
    });

    const replies = await this.gather<PrepareReply>({ type: 'prepare', ballot });
    const oks = replies.filter((r): r is PrepareReply => r !== null && r.ok);
    if (oks.length < this.majority) return false;

    const becameLeader = await this.mutex.run(() => {
      // Abort if a higher promise was made meanwhile, or a newer local
      // campaign already won leadership.
      if (compareBallots(this.promised, ballot) > 0) return false;
      if (this.leaderBallot && compareBallots(this.leaderBallot, ballot) > 0) return false;
      this.role = 'leader';
      this.leaderBallot = ballot;
      this.leaseExpiry = this.now() + this.leaseMs;
      return true;
    });
    if (!becameLeader) return false;

    await this.repair(ballot, oks);
    return this.role === 'leader' && sameBallot(this.leaderBallot, ballot);
  }

  /** Fill every uncommitted slot up to the highest known slot, in order. */
  private async repair(ballot: Ballot, oks: PrepareReply[]): Promise<void> {
    let maxSlot = 0;
    const chosen = new Map<number, LogEntry>();
    const consider = (slot: number, e: LogEntry) => {
      if (slot > maxSlot) maxSlot = slot;
      const cur = chosen.get(slot);
      if (!cur || compareBallots(e.ballot, cur.ballot) > 0) chosen.set(slot, e);
    };
    for (const r of oks) {
      for (const key of Object.keys(r.log)) consider(Number(key), r.log[Number(key)]);
    }
    const start = await this.mutex.run(() => this.committed + 1);
    for (let s = start; s <= maxSlot; s++) {
      const e = chosen.get(s);
      // Highest-ballot entry wins; an empty slot is a hole -> no-op filler.
      const ok = await this.acceptAndCommit(s, ballot, e ? e.value : null);
      if (!ok) return; // lost leadership mid-repair
    }
  }

  /**
   * Phase 2 for one slot; on majority acceptance marks the entry committed,
   * advances the watermark and broadcasts commit notices.
   */
  private async acceptAndCommit(
    slot: number,
    ballot: Ballot,
    value: Command | null,
  ): Promise<boolean> {
    const msg: AcceptMsg = { type: 'accept', slot, ballot, value };
    const replies = await this.gather<AcceptReply>(msg);
    const oks = replies.filter((r) => r !== null && r.ok).length;
    if (oks < this.majority) {
      await this.mutex.run(() => {
        if (
          sameBallot(this.leaderBallot, ballot) &&
          compareBallots(this.highestSeen, ballot) > 0
        ) {
          this.role = 'follower';
          this.leaseExpiry = 0;
        }
      });
      return false;
    }
    const notices = await this.mutex.run(() => {
      const e = this.log.get(slot);
      if (!e || !sameValue(e.value, value)) {
        this.safetyViolations.push(`slot ${slot}: own accepted entry missing after majority`);
        return null;
      }
      const before = this.committed;
      e.committed = true;
      this.advanceWatermark();
      if (this.committed === before) return null;
      const newly: Array<{ slot: number; entry: LogEntry }> = [];
      for (let s = before + 1; s <= this.committed; s++) {
        newly.push({ slot: s, entry: this.log.get(s)! });
      }
      return { newly, committed: this.committed };
    });
    if (notices) {
      for (const n of notices.newly) {
        for (const p of this.peers) {
          if (p === this.id) continue;
          this.stats.commitSent++;
          const notice: CommitMsg = {
            type: 'commit',
            slot: n.slot,
            entry: n.entry,
            committed: notices.committed,
          };
          this.net.notify(this.id, p, notice);
        }
      }
    }
    return true;
  }

  /**
   * Propose a client command. Uses the leader lease to skip Phase 1 when
   * valid; re-runs Phase 1 after lease expiry. Idempotent per command id
   * (clientId, seq): retries return the originally assigned slot.
   */
  async propose(value: Command): Promise<number> {
    for (let attempt = 0; attempt < 3; attempt++) {
      const st = await this.mutex.run(() => {
        for (const [slot, e] of this.log) {
          if (e.value && e.value.clientId === value.clientId && e.value.seq === value.seq) {
            return { kind: 'dup' as const, slot, committed: e.committed };
          }
        }
        if (this.role !== 'leader' || !this.leaderBallot) {
          return { kind: 'notLeader' as const };
        }
        if (this.now() >= this.leaseExpiry) return { kind: 'expired' as const };
        const slot = ++this.tail;
        return { kind: 'ok' as const, slot, ballot: this.leaderBallot };
      });

      switch (st.kind) {
        case 'dup': {
          if (st.committed) return st.slot;
          const ctx = await this.mutex.run(() =>
            this.role === 'leader' && this.leaderBallot
              ? { ballot: this.leaderBallot, value: this.log.get(st.slot)?.value ?? value }
              : null,
          );
          if (!ctx) throw new NotLeaderError(this.id);
          if (!(await this.acceptAndCommit(st.slot, ctx.ballot, ctx.value))) {
            throw new NotLeaderError(this.id);
          }
          return st.slot;
        }
        case 'notLeader':
          throw new NotLeaderError(this.id);
        case 'expired': {
          if (!(await this.campaign())) throw new NotLeaderError(this.id);
          break; // retry with the fresh lease
        }
        case 'ok': {
          if (!(await this.acceptAndCommit(st.slot, st.ballot, value))) {
            throw new NotLeaderError(this.id);
          }
          return st.slot;
        }
      }
    }
    throw new NotLeaderError(this.id);
  }

  /** Re-broadcast commit notices for the whole committed prefix (heartbeat). */
  async broadcastCommits(): Promise<void> {
    const snap = await this.mutex.run(() => {
      const out: Array<{ slot: number; entry: LogEntry }> = [];
      for (let s = 1; s <= this.committed; s++) {
        const e = this.log.get(s);
        if (e) out.push({ slot: s, entry: e });
      }
      return { out, committed: this.committed };
    });
    for (const n of snap.out) {
      for (const p of this.peers) {
        if (p === this.id) continue;
        this.stats.commitSent++;
        const notice: CommitMsg = {
          type: 'commit',
          slot: n.slot,
          entry: n.entry,
          committed: snap.committed,
        };
        this.net.notify(this.id, p, notice);
      }
    }
  }
}
