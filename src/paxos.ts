// Multi-Paxos majority consensus over a persistent, gap-free log.
//
//  - Phase 1 (Prepare/Promise) elects a leader for a ballot and collects
//    every acceptor's uncommitted suffix so the leader can repair gaps and
//    re-propose already-chosen values (safety) or no-ops (hole filling).
//  - Phase 2 (Accept/Accepted) replicates one slot to a majority.
//  - While the leader lease is valid, Phase 1 is skipped and consecutive
//    LogEntries commit directly through Phase 2.
//  - Acceptors overwrite uncommitted "dirty" minority entries when a
//    higher-ballot Accept arrives (truncate suffix + rewrite).

import { Buffer } from 'node:buffer';
import { EntryType, type EntryView } from './codec.ts';
import type { LogStore } from './logstore.ts';
import type { Message, Transport } from './transport.ts';

export interface Ballot {
  term: number;
  node: number;
}

export function compareBallot(a: Ballot, b: Ballot): number {
  return a.term - b.term || a.node - b.node;
}

function ballotEq(a: Ballot, b: Ballot): boolean {
  return a.term === b.term && a.node === b.node;
}

export function encodeBallot(b: Ballot): bigint {
  return BigInt(b.term) * 0x1_0000_0000n + BigInt(b.node);
}

interface AcceptedRecord {
  index: number;
  term: bigint;
  type: number;
  payload: Buffer;
}

export interface PaxosOptions {
  leaseMs?: number;
  rpcTimeoutMs?: number;
  onCommit?: (nodeId: number, index: number, view: EntryView) => void;
}

type Timer = ReturnType<typeof setTimeout>;

export class PaxosNode {
  readonly id: number;
  private readonly store: LogStore;
  private readonly transport: Transport;
  private readonly peers: number[];
  private readonly quorum: number;
  private readonly leaseMs: number;
  private readonly rpcTimeoutMs: number;
  private readonly onCommitCb?: PaxosOptions['onCommit'];

  private promised: Ballot = { term: 0, node: 0 };
  private ballot: Ballot = { term: 0, node: 0 };
  private leadBallot: Ballot | null = null;
  private role: 'follower' | 'leader' = 'follower';
  private leaseUntil = 0;
  private committed = 0;
  private nextIndex = 1;

  private readonly chosen = new Set<number>();
  private readonly listeners = new Map<number, (msg: Message) => void>();
  private readonly pendingRpcs = new Set<(value: Message[] | null) => void>();
  private readonly commitWaiters: Array<{ index: number; resolve: () => void }> = [];
  private readonly timers = new Set<Timer>();
  private rpcSeq = 0;
  private acceptChain: Promise<unknown> = Promise.resolve();
  private campaigning: Promise<boolean> | null = null;
  private stopped = false;

  constructor(id: number, store: LogStore, transport: Transport, peers: number[], opts: PaxosOptions = {}) {
    this.id = id;
    this.store = store;
    this.transport = transport;
    this.peers = peers;
    this.quorum = Math.floor(peers.length / 2) + 1;
    this.leaseMs = opts.leaseMs ?? 5000;
    this.rpcTimeoutMs = opts.rpcTimeoutMs ?? 300;
    this.onCommitCb = opts.onCommit;
    transport.register(id, (msg) => this.handle(msg));
  }

  get currentRole(): 'follower' | 'leader' {
    return this.role;
  }

  get commitIndex(): number {
    return this.committed;
  }

  get currentBallot(): Ballot {
    return this.ballot;
  }

  get lastIndex(): number {
    return this.store.lastIndex;
  }

  get activeTimers(): number {
    return this.timers.size;
  }

  entryAt(index: number): EntryView | undefined {
    return this.store.at(index);
  }

  // Failure-injection hook for tests: append an accepted-but-uncommitted
  // entry directly, simulating a replica that crashed mid-replication.
  injectUncommitted(term: bigint, index: number, type: number, payload: Buffer): Promise<EntryView> {
    return this.store.append({ term, index, type, payload });
  }

  // ---------------------------------------------------------------- leader

  async campaign(): Promise<boolean> {
    if (this.campaigning) return this.campaigning;
    this.campaigning = this.doCampaign().finally(() => {
      this.campaigning = null;
    });
    return this.campaigning;
  }

  private async doCampaign(): Promise<boolean> {
    this.assertActive();
    const ballot: Ballot = { term: Math.max(this.ballot.term, this.promised.term) + 1, node: this.id };
    const replies = await this.collectQuorum(
      'prepare',
      { ballot, commitIndex: this.committed },
      'promise',
      (m) => ballotEq(m.ballot as Ballot, ballot) && m.ok === true,
      (m) => {
        const p = m.promised as Ballot | undefined;
        if (p && compareBallot(p, this.ballot) > 0) this.ballot = p;
      },
    );
    if (!replies) return false;
    this.ballot = ballot;
    this.leadBallot = ballot;
    this.role = 'leader';
    this.leaseUntil = Date.now() + this.leaseMs;
    await this.adoptCommittedPrefix(replies);
    await this.repair(replies);
    return true;
  }

  // Adopt the longest committed prefix reported by the quorum so values
  // chosen before the failover can never be overwritten by this leader.
  private async adoptCommittedPrefix(promises: Message[]): Promise<void> {
    let maxCommit = this.committed;
    for (const p of promises) maxCommit = Math.max(maxCommit, p.commitIndex as number);
    for (let i = this.committed + 1; i <= maxCommit; i++) {
      const reporter = promises
        .filter((p) => (p.commitIndex as number) >= i)
        .sort((a, b) => (b.commitIndex as number) - (a.commitIndex as number))[0];
      const rec = ((reporter.accepted as AcceptedRecord[] | undefined) ?? []).find((e) => e.index === i);
      if (!rec) throw new Error(`committed slot ${i} missing from quorum promises`);
      const existing = this.store.at(i);
      if (existing && (existing.term !== rec.term || !existing.payload.equals(rec.payload))) {
        await this.store.truncateSuffix(i);
      }
      if (!this.store.at(i)) {
        await this.store.append({ term: rec.term, index: i, type: rec.type, payload: rec.payload });
      }
      this.committed = i;
      this.deliver(i);
    }
    if (this.committed > 0) {
      for (const p of this.peers) {
        this.send(p, 'commit', { commitIndex: this.committed, commitTerm: this.termAt(this.committed) });
      }
    }
  }

  // Phase-1 driven catch-up: re-propose chosen values, fill holes with no-op.
  private async repair(promises: Message[]): Promise<void> {
    const chosenValues = new Map<number, { term: bigint; type: number; payload: Buffer }>();
    let maxIdx = this.committed;
    for (const p of promises) {
      const accepted = (p.accepted as AcceptedRecord[] | undefined) ?? [];
      for (const e of accepted) {
        if (e.index > maxIdx) maxIdx = e.index;
        const cur = chosenValues.get(e.index);
        if (!cur || e.term > cur.term) {
          chosenValues.set(e.index, { term: e.term, type: e.type, payload: e.payload });
        }
      }
    }
    for (let i = this.committed + 1; i <= maxIdx; i++) {
      const e = chosenValues.get(i) ?? { term: 0n, type: EntryType.Noop, payload: Buffer.alloc(0) };
      const ok = await this.proposeAt(i, e.type, e.payload);
      if (!ok) throw new Error(`repair failed at slot ${i}`);
    }
    this.nextIndex = maxIdx + 1;
  }

  async propose(payload: Buffer): Promise<number> {
    this.assertActive();
    if (this.role !== 'leader') throw new Error(`node ${this.id} is not leader`);
    if (Date.now() >= this.leaseUntil) {
      const ok = await this.campaign();
      if (!ok || this.role !== 'leader') throw new Error(`node ${this.id} lost leadership`);
    }
    const index = this.nextIndex++;
    const ok = await this.proposeAt(index, EntryType.Data, payload);
    if (!ok) throw new Error(`proposal at slot ${index} was rejected`);
    await this.waitCommit(index);
    return index;
  }

  private async proposeAt(index: number, type: number, payload: Buffer): Promise<boolean> {
    const ballot = this.leadBallot as Ballot;
    const term = encodeBallot(ballot);
    // Persist locally before broadcasting so the prevTerm of this and all
    // subsequent slots reflects the leader's real log tail.
    await this.enqueueAccept(() => this.applyAccepted(term, index, type, payload));
    const replies = await this.collectQuorum(
      'accept',
      {
        ballot,
        index,
        term,
        type,
        payload: Buffer.from(payload),
        prevIndex: index - 1,
        prevTerm: this.termAt(index - 1),
      },
      'accepted',
      (m) => m.index === index && m.ok === true && ballotEq(m.ballot as Ballot, ballot),
      (m) => {
        const p = m.promised as Ballot | undefined;
        if (p && compareBallot(p, this.ballot) > 0) this.ballot = p;
        if (m.needSync === true) void this.syncFollower(m.from, (m.commitIndex as number) + 1);
      },
    );
    if (!replies) return false;
    this.markChosen(index);
    return true;
  }

  private markChosen(index: number): void {
    this.chosen.add(index);
    while (this.chosen.has(this.committed + 1)) {
      this.chosen.delete(this.committed + 1);
      this.committed += 1;
      this.deliver(this.committed);
      for (const p of this.peers) {
        this.send(p, 'commit', { commitIndex: this.committed, commitTerm: this.termAt(this.committed) });
      }
    }
    for (let i = this.commitWaiters.length - 1; i >= 0; i--) {
      if (this.commitWaiters[i].index <= this.committed) {
        this.commitWaiters[i].resolve();
        this.commitWaiters.splice(i, 1);
      }
    }
  }

  private waitCommit(index: number): Promise<void> {
    if (this.committed >= index) return Promise.resolve();
    return new Promise((resolve) => this.commitWaiters.push({ index, resolve }));
  }

  // -------------------------------------------------------------- acceptor

  private handle(msg: Message): void {
    if (this.stopped) return;
    switch (msg.kind) {
      case 'prepare':
        this.onPrepare(msg);
        break;
      case 'accept':
        this.onAccept(msg);
        break;
      case 'commit':
        // Serialize with accepts so a commit is only evaluated after all
        // previously received entries have been applied to the log.
        void this.enqueueAccept(async () => {
          this.onCommitMsg(msg);
        });
        break;
      default:
        for (const fn of [...this.listeners.values()]) fn(msg);
    }
  }

  private onPrepare(msg: Message): void {
    const ballot = msg.ballot as Ballot;
    if (compareBallot(ballot, this.promised) <= 0) {
      this.send(msg.from, 'promise', { ballot, ok: false, promised: this.promised });
      return;
    }
    this.promised = ballot;
    if (compareBallot(ballot, this.ballot) > 0) this.ballot = ballot;
    if (this.role === 'leader' && this.leadBallot && compareBallot(ballot, this.leadBallot) > 0) {
      this.stepDown();
    }
    const accepted: AcceptedRecord[] = [];
    const from = Math.max(1, (msg.commitIndex as number) + 1);
    for (let i = from; i <= this.store.lastIndex; i++) {
      const v = this.store.at(i);
      if (v) accepted.push({ index: i, term: v.term, type: v.type, payload: Buffer.from(v.payload) });
    }
    this.send(msg.from, 'promise', { ballot, ok: true, accepted, commitIndex: this.committed });
  }

  private onAccept(msg: Message): void {
    const ballot = msg.ballot as Ballot;
    const index = msg.index as number;
    if (compareBallot(ballot, this.promised) < 0) {
      this.send(msg.from, 'accepted', { ballot, index, ok: false, promised: this.promised });
      return;
    }
    const term = msg.term as bigint;
    const type = msg.type as number;
    const payload = msg.payload as Buffer;
    const prevIndex = msg.prevIndex as number;
    const prevTerm = msg.prevTerm as bigint;
    void this.enqueueAccept(async () => {
      // Re-validate inside the serialized chain: a later Prepare may have
      // raised our promise while this accept was queued.
      if (compareBallot(ballot, this.promised) < 0) {
        this.send(msg.from, 'accepted', { ballot, index, ok: false, promised: this.promised });
        return;
      }
      const localPrev = this.termAt(prevIndex);
      if (localPrev === undefined || localPrev !== prevTerm) {
        // Log mismatch (hole or dirty suffix): ask the leader to backfill.
        this.send(msg.from, 'accepted', {
          ballot,
          index,
          ok: false,
          needSync: true,
          commitIndex: this.committed,
        });
        return;
      }
      this.promised = ballot;
      if (compareBallot(ballot, this.ballot) > 0) this.ballot = ballot;
      if (this.role === 'leader' && this.leadBallot && compareBallot(ballot, this.leadBallot) > 0) {
        this.stepDown();
      }
      try {
        await this.applyAccepted(term, index, type, payload);
        this.send(msg.from, 'accepted', { ballot, index, ok: true });
      } catch {
        this.send(msg.from, 'accepted', { ballot, index, ok: false, promised: this.promised });
      }
    });
  }

  // Idempotently install an accepted entry, truncating any conflicting
  // uncommitted ("dirty") suffix first.
  private async applyAccepted(term: bigint, index: number, type: number, payload: Buffer): Promise<void> {
    const existing = this.store.at(index);
    if (existing && existing.term === term && existing.payload.equals(payload)) return;
    if (existing) await this.store.truncateSuffix(index);
    if (!this.store.has(index)) {
      await this.store.append({ term, index, type, payload });
    }
  }

  private enqueueAccept<T>(fn: () => Promise<T>): Promise<T> {
    const p = this.acceptChain.then(fn);
    this.acceptChain = p.catch(() => {});
    return p;
  }

  private onCommitMsg(msg: Message): void {
    const target = msg.commitIndex as number;
    if (target <= this.committed) return;
    const local = this.termAt(target);
    if (local === undefined || local !== (msg.commitTerm as bigint)) {
      // Our suffix diverges from the leader's committed log; a backfill
      // followed by a fresh commit notification will resolve this.
      return;
    }
    while (this.committed < target) {
      this.committed += 1;
      this.deliver(this.committed);
    }
  }

  // Bring a lagging/diverged follower up to date from its committed prefix.
  private syncFollower(follower: number, from: number): void {
    void this.syncFollowerAsync(follower, from);
  }

  private async syncFollowerAsync(follower: number, from: number): Promise<void> {
    await this.acceptChain;
    if (this.role !== 'leader' || !this.leadBallot || this.stopped) return;
    const to = this.store.lastIndex;
    for (let i = Math.max(1, from); i <= to; i++) {
      const v = this.store.at(i);
      if (!v) continue;
      this.send(follower, 'accept', {
        ballot: this.leadBallot,
        index: i,
        term: v.term,
        type: v.type,
        payload: Buffer.from(v.payload),
        prevIndex: i - 1,
        prevTerm: this.termAt(i - 1),
      });
    }
    this.send(follower, 'commit', { commitIndex: this.committed, commitTerm: this.termAt(this.committed) });
  }

  private termAt(index: number): bigint | undefined {
    if (index === 0) return 0n;
    return this.store.at(index)?.term;
  }

  private stepDown(): void {
    this.role = 'follower';
    this.leadBallot = null;
    this.leaseUntil = 0;
  }

  private deliver(index: number): void {
    const view = this.store.at(index);
    if (view && this.onCommitCb) this.onCommitCb(this.id, index, view);
  }

  // ------------------------------------------------------------------ misc

  private collectQuorum(
    sendKind: string,
    payload: Record<string, unknown>,
    replyKind: string,
    accept: (m: Message) => boolean,
    onReject?: (m: Message) => void,
  ): Promise<Message[] | null> {
    return new Promise((resolve) => {
      const got: Message[] = [];
      const id = ++this.rpcSeq;
      let done = false;
      const finish = (value: Message[] | null) => {
        if (done) return;
        done = true;
        this.listeners.delete(id);
        this.pendingRpcs.delete(finish);
        clearTimeout(timer);
        this.timers.delete(timer);
        resolve(value);
      };
      const timer = setTimeout(() => finish(null), this.rpcTimeoutMs);
      this.timers.add(timer);
      this.pendingRpcs.add(finish);
      this.listeners.set(id, (m) => {
        if (m.kind !== replyKind) return;
        if (!accept(m)) {
          onReject?.(m);
          return;
        }
        got.push(m);
        if (got.length >= this.quorum) finish(got);
      });
      for (const p of this.peers) this.send(p, sendKind, payload);
    });
  }

  private send(to: number, kind: string, payload: Record<string, unknown>): void {
    this.transport.send({ kind, from: this.id, to, ...payload });
  }

  private assertActive(): void {
    if (this.stopped) throw new Error(`node ${this.id} is stopped`);
  }

  async stop(): Promise<void> {
    if (this.stopped) return;
    this.stopped = true;
    for (const finish of [...this.pendingRpcs]) finish(null);
    for (const t of this.timers) clearTimeout(t);
    for (const w of this.commitWaiters.splice(0)) w.resolve();
    this.timers.clear();
    this.listeners.clear();
    this.transport.unregister(this.id);
    await this.acceptChain.catch(() => {});
    await this.store.close();
  }
}
