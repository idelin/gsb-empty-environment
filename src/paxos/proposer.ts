import { ENTRY_NOOP, ENTRY_VALUE, makeEntry, type LogEntry } from '../codec/logEntry.ts';
import type { LogStore } from '../storage/logStore.ts';
import type { SimulatedNetwork } from './network.ts';
import {
  compareBallot,
  MSG_ACCEPT,
  MSG_COMMIT,
  MSG_PREPARE,
  NoMajorityError,
  NotLeaderError,
  ZERO_BALLOT,
  type AcceptReq,
  type AcceptRes,
  type Ballot,
  type CommitNotice,
  type PrepareReq,
  type PrepareRes,
} from './types.ts';

export interface Clock {
  now(): number;
}

export class LogicalClock implements Clock {
  #t: number;
  constructor(start = 0) {
    this.#t = start;
  }
  now(): number {
    return this.#t;
  }
  advance(ms: number): void {
    this.#t += ms;
  }
}

export interface ProposerOptions {
  leaseMs: number;
  clock: Clock;
}

/**
 * Multi-Paxos proposer with a leader lease: after one successful Phase 1
 * (Prepare/Promise) the leader holds a lease and commits a run of
 * consecutive LogEntries through Phase 2 (Accept/Accepted) alone. Lease
 * expiry or a higher promised ballot forces a new Phase 1, which also
 * repairs gaps: slots seen in promises are re-proposed with the
 * highest-ballot value; slots with no accepted value are filled with no-ops,
 * so the committed log never has holes. Accepts carry prevBallot for log
 * matching; followers that diverge are backfilled from the leader's log.
 */
export class Proposer {
  #ballot: Ballot | null = null;
  #prepared = false;
  #leaseUntil = 0;
  #commitIndex = 0;
  #committed = new Set<number>();
  #nextIndex = 1;
  #maxSeenTerm: number;
  #chain: Promise<unknown> = Promise.resolve();
  #inFlightCount = 0;

  readonly id: number;
  readonly peers: number[];
  readonly network: SimulatedNetwork;
  readonly store: LogStore;
  readonly options: ProposerOptions;

  constructor(id: number, peers: number[], network: SimulatedNetwork, store: LogStore, options: ProposerOptions) {
    this.id = id;
    this.peers = peers;
    this.network = network;
    this.store = store;
    this.options = options;
    this.#maxSeenTerm = store.promised.term;
    this.#commitIndex = store.commitIndex;
  }

  get majority(): number {
    return Math.floor(this.peers.length / 2) + 1;
  }

  get isLeader(): boolean {
    return this.#prepared && this.options.clock.now() < this.#leaseUntil;
  }

  get ballot(): Ballot | null {
    return this.#ballot;
  }

  get commitIndex(): number {
    return this.#commitIndex;
  }

  get nextIndex(): number {
    return this.#nextIndex;
  }

  get inFlight(): number {
    return this.#inFlightCount;
  }

  /** Serialize client proposals: indexes are handed out under one chain. */
  propose(value: Buffer): Promise<number> {
    const run = this.#chain.then(() => this.#proposeSerialized(value));
    this.#chain = run.then(
      () => undefined,
      () => undefined,
    );
    return run;
  }

  async #proposeSerialized(value: Buffer): Promise<number> {
    const maxAttempts = 8;
    for (let attempt = 1; ; attempt++) {
      if (!this.isLeader) await this.#runPhase1();
      const index = this.#nextIndex++;
      try {
        await this.#acceptMajority(index, ENTRY_VALUE, value);
        return index;
      } catch (err) {
        // A higher ballot appeared mid-flight: back off (deterministically
        // jittered per node and attempt) and retry Phase 1, so duelling
        // proposers cannot livelock.
        if (!(err instanceof NotLeaderError)) throw err;
        if (attempt >= maxAttempts) throw new NoMajorityError(`gave up after ${attempt} contested ballots`);
        const backoffMs = attempt * 2 + ((this.id * 7 + attempt * 13) % 5);
        await new Promise((resolve) => setTimeout(resolve, backoffMs));
      }
    }
  }

  /** Phase 1: Prepare/Promise + gap repair of every uncommitted slot. */
  async #runPhase1(): Promise<void> {
    const ballot: Ballot = {
      term: Math.max(this.#maxSeenTerm, this.store.promised.term) + 1,
      nodeId: this.id,
    };
    const req: PrepareReq = { ballot };
    const replies = await this.#broadcast<PrepareRes>(MSG_PREPARE, req);

    let oks = 0;
    const byIndex = new Map<number, LogEntry>();
    for (const res of replies) {
      if (res.status !== 'fulfilled') continue;
      const r = res.value;
      this.#observeBallot(r.promised);
      if (!r.ok) continue;
      oks++;
      for (const rec of r.accepted) {
        const cur = byIndex.get(rec.index);
        if (!cur || compareBallot({ term: rec.term, nodeId: rec.nodeId }, { term: cur.term, nodeId: cur.nodeId }) > 0) {
          byIndex.set(rec.index, rec);
        }
      }
    }
    if (oks < this.majority) {
      this.#prepared = false;
      throw new NoMajorityError(`prepare reached ${oks}/${this.peers.length} acceptors`);
    }

    this.#ballot = ballot;
    this.#prepared = true;
    this.#leaseUntil = this.options.clock.now() + this.options.leaseMs;

    // Repair: re-propose the highest-ballot value per slot; fill holes with
    // no-ops so the commit index can advance without gaps. Minority dirty
    // entries are safely overwritten by the new ballot.
    let maxIdx = 0;
    for (const i of byIndex.keys()) if (i > maxIdx) maxIdx = i;
    for (let i = this.#commitIndex + 1; i <= maxIdx; i++) {
      const rec = byIndex.get(i);
      if (rec) {
        await this.#acceptMajority(i, rec.type, rec.value);
      } else {
        await this.#acceptMajority(i, ENTRY_NOOP, Buffer.alloc(0));
      }
    }
    this.#nextIndex = maxIdx + 1;
  }

  /**
   * Phase 2 for one slot, with follower catch-up: if a follower reports
   * divergence (needSyncFrom), walk back and re-accept the leader's entries
   * from that slot upward until the whole prefix matches and `index` is
   * majority-accepted.
   */
  async #acceptMajority(index: number, type: number, value: Buffer): Promise<void> {
    const ballot = this.#ballot!;
    const fresh = makeEntry(index, ballot.term, ballot.nodeId, type, value, 0, this.options.clock.now());
    let slot = index;
    for (;;) {
      const entry = slot === index ? fresh : this.store.get(slot);
      if (!entry) throw new NoMajorityError(`leader is missing its own slot ${slot}`);
      const prev = slot > 1 ? this.store.get(slot - 1) : undefined;
      const req: AcceptReq = {
        ballot,
        entry,
        prevBallot: prev ? { term: prev.term, nodeId: prev.nodeId } : { ...ZERO_BALLOT },
      };
      const replies = await this.#broadcast<AcceptRes>(MSG_ACCEPT, req);

      let oks = 0;
      let sawHigher = false;
      let syncFrom = Number.POSITIVE_INFINITY;
      for (const res of replies) {
        if (res.status !== 'fulfilled') continue;
        const r = res.value;
        this.#observeBallot(r.promised);
        if (r.ok) {
          oks++;
        } else {
          if (compareBallot(r.promised, ballot) > 0) sawHigher = true;
          if (r.needSyncFrom !== undefined && r.needSyncFrom < syncFrom) syncFrom = r.needSyncFrom;
        }
      }
      if (sawHigher) {
        this.#prepared = false;
        throw new NotLeaderError();
      }
      if (syncFrom < slot) {
        slot = syncFrom; // a follower diverged earlier: backfill from there
        continue;
      }
      if (oks < this.majority) {
        this.#prepared = false;
        throw new NoMajorityError(`accept for slot ${slot} reached ${oks}/${this.peers.length} acceptors`);
      }
      if (slot === index) {
        this.#committed.add(index);
        await this.#advanceCommit();
        return;
      }
      slot++; // backfilled slot done: walk back up toward `index`
    }
  }

  async #advanceCommit(): Promise<void> {
    let advanced = false;
    while (this.#committed.has(this.#commitIndex + 1)) {
      this.#commitIndex++;
      advanced = true;
    }
    if (!advanced) return;
    await this.store.setCommitIndex(this.#commitIndex);
    const notice: CommitNotice = { commitIndex: this.#commitIndex };
    await this.#broadcast<unknown>(MSG_COMMIT, notice); // best-effort catch-up
  }

  #observeBallot(b: Ballot): void {
    if (b.term > this.#maxSeenTerm) this.#maxSeenTerm = b.term;
  }

  async #broadcast<T>(type: typeof MSG_PREPARE | typeof MSG_ACCEPT | typeof MSG_COMMIT, payload: unknown) {
    this.#inFlightCount++;
    try {
      return await Promise.allSettled(this.peers.map((p) => this.network.rpc(this.id, p, type, payload) as Promise<T>));
    } finally {
      this.#inFlightCount--;
    }
  }
}
