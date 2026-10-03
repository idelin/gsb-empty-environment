import type { LogEntry } from '../codec/logEntry.ts';
import type { LogStore } from '../storage/logStore.ts';
import type { Clock } from './proposer.ts';
import {
  compareBallot,
  sameBallot,
  ZERO_BALLOT,
  type AcceptReq,
  type AcceptRes,
  type Ballot,
  type PrepareReq,
  type PrepareRes,
} from './types.ts';

/**
 * Acceptor: persists the promised ballot and every accepted entry before
 * replying, so a restarted node never violates a promise. Accepts enforce
 * log matching (prevBallot): a follower never holds a gap or a divergent
 * prefix, which is what makes the leader's commit index safe to follow.
 */
export class Acceptor {
  readonly id: number;
  readonly store: LogStore;
  #clock: Clock;
  #leaseMs: number;
  #leaseUntil = 0;

  constructor(id: number, store: LogStore, clock: Clock, leaseMs: number) {
    this.id = id;
    this.store = store;
    this.#clock = clock;
    this.#leaseMs = leaseMs;
  }

  async onPrepare(req: PrepareReq): Promise<PrepareRes> {
    const { ballot } = req;
    if (compareBallot(ballot, this.store.promised) < 0) {
      return { ok: false, promised: { ...this.store.promised }, accepted: [] };
    }
    // Leader lease: while a previously granted lease is live, reject every
    // Prepare (even higher ballots). Failover waits for lease expiry, which
    // is what stops duelling proposers from preempting each other.
    if (this.#clock.now() < this.#leaseUntil) {
      return { ok: false, promised: { ...this.store.promised }, accepted: [] };
    }
    await this.store.setPromised(ballot);
    this.#leaseUntil = this.#clock.now() + this.#leaseMs;
    return {
      ok: true,
      promised: { ...ballot },
      accepted: this.store.entriesFrom(1),
    };
  }

  async onAccept(req: AcceptReq): Promise<AcceptRes> {
    const { ballot, entry } = req;
    if (compareBallot(ballot, this.store.promised) < 0) {
      return { ok: false, promised: { ...this.store.promised } };
    }
    // Log matching: the slot before `entry.index` must exist and carry the
    // same ballot the leader has for it. Otherwise ask for backfill.
    if (entry.index > 1) {
      const prev = this.store.get(entry.index - 1);
      const prevBallot: Ballot = prev ? { term: prev.term, nodeId: prev.nodeId } : { ...ZERO_BALLOT };
      if (!prev || !sameBallot(prevBallot, req.prevBallot)) {
        return { ok: false, promised: { ...this.store.promised }, needSyncFrom: entry.index - 1 };
      }
    }
    // Persist before acknowledging; overwriting a dirty slot under a higher
    // ballot is safe because the old value was never majority-committed.
    await this.store.accept(entry);
    return { ok: true, promised: { ...this.store.promised } };
  }
}

export function ballotOf(entry: LogEntry): Ballot {
  return { term: entry.term, nodeId: entry.nodeId };
}
