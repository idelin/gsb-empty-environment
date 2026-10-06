export type EventId = string;

export interface ScoreEvent<U> {
  readonly kind: "score";
  readonly id: EventId;
  readonly userId: U;
  readonly delta: bigint;
}

export interface RevokeEvent {
  readonly kind: "revoke";
  readonly id: EventId;
}

export type LeaderboardEvent<U> = ScoreEvent<U> | RevokeEvent;

export interface RankedEntry<U> {
  readonly rank: number;
  readonly userId: U;
  readonly score: bigint;
}

export interface MovedEntry<U> {
  readonly userId: U;
  readonly from: number;
  readonly to: number;
  readonly score: bigint;
}

export interface TopKDiff<U> {
  readonly entered: ReadonlyArray<RankedEntry<U>>;
  readonly left: ReadonlyArray<RankedEntry<U>>;
  readonly moved: ReadonlyArray<MovedEntry<U>>;
}

export interface ApplyResult<U> {
  readonly accepted: boolean;
  readonly diff: TopKDiff<U>;
}

export type EventState = "live" | "revoked" | "unknown";

const EMPTY_DIFF: TopKDiff<never> = { entered: [], left: [], moved: [] };

function defaultCompareUserIds<U>(a: U, b: U): number {
  if (typeof a === "number" && typeof b === "number") {
    return a < b ? -1 : a > b ? 1 : 0;
  }
  const sa = String(a);
  const sb = String(b);
  return sa < sb ? -1 : sa > sb ? 1 : 0;
}

interface SortedEntry<U> {
  userId: U;
  score: bigint;
}

export class Leaderboard<U = string> {
  private readonly k: number;
  private readonly compareUserIds: (a: U, b: U) => number;
  private readonly scores = new Map<U, bigint>();
  private sorted: Array<SortedEntry<U>> = [];
  private readonly live = new Map<EventId, { userId: U; delta: bigint }>();
  private readonly revoked = new Set<EventId>();
  private topK: Array<RankedEntry<U>> = [];

  constructor(k: number, compareUserIds?: (a: U, b: U) => number) {
    if (!Number.isInteger(k) || k < 0) {
      throw new RangeError(`k must be a non-negative integer, got ${k}`);
    }
    this.k = k;
    this.compareUserIds = compareUserIds ?? defaultCompareUserIds;
  }

  apply(event: LeaderboardEvent<U>): ApplyResult<U> {
    if (event.kind === "score") {
      if (this.revoked.has(event.id) || this.live.has(event.id)) {
        return { accepted: false, diff: EMPTY_DIFF };
      }
      this.live.set(event.id, { userId: event.userId, delta: event.delta });
      this.addDelta(event.userId, event.delta);
    } else {
      if (this.revoked.has(event.id)) {
        return { accepted: false, diff: EMPTY_DIFF };
      }
      this.revoked.add(event.id);
      const liveEvent = this.live.get(event.id);
      if (liveEvent !== undefined) {
        this.live.delete(event.id);
        this.addDelta(liveEvent.userId, -liveEvent.delta);
      }
    }
    const nextTopK = this.computeTopK();
    const diff = this.computeDiff(this.topK, nextTopK);
    this.topK = nextTopK;
    return { accepted: true, diff };
  }

  eventState(id: EventId): EventState {
    if (this.live.has(id)) return "live";
    if (this.revoked.has(id)) return "revoked";
    return "unknown";
  }

  getScore(userId: U): bigint {
    return this.scores.get(userId) ?? 0n;
  }

  getTopK(): ReadonlyArray<RankedEntry<U>> {
    return this.topK.map((entry) => ({ ...entry }));
  }

  rankOf(userId: U): number | null {
    const score = this.scores.get(userId);
    if (score === undefined) return null;
    const index = this.findIndex(score, userId);
    return index === -1 ? null : index + 1;
  }

  snapshot(): ReadonlyArray<RankedEntry<U>> {
    return this.sorted.map((entry, index) => ({
      rank: index + 1,
      userId: entry.userId,
      score: entry.score,
    }));
  }

  static replay<U>(
    events: Iterable<LeaderboardEvent<U>>,
    k: number,
    compareUserIds?: (a: U, b: U) => number,
  ): Leaderboard<U> {
    const board = new Leaderboard<U>(k, compareUserIds);
    for (const event of events) {
      board.apply(event);
    }
    return board;
  }

  private addDelta(userId: U, delta: bigint): void {
    const oldScore = this.scores.get(userId);
    if (oldScore !== undefined) {
      const index = this.findIndex(oldScore, userId);
      this.sorted.splice(index, 1);
    }
    const newScore = (oldScore ?? 0n) + delta;
    this.scores.set(userId, newScore);
    const insertAt = this.lowerBound(newScore, userId);
    this.sorted.splice(insertAt, 0, { userId, score: newScore });
  }

  private compareEntries(a: SortedEntry<U>, b: SortedEntry<U>): number {
    if (a.score > b.score) return -1;
    if (a.score < b.score) return 1;
    return this.compareUserIds(a.userId, b.userId);
  }

  private lowerBound(score: bigint, userId: U): number {
    const target: SortedEntry<U> = { userId, score };
    let lo = 0;
    let hi = this.sorted.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.compareEntries(this.sorted[mid], target) < 0) {
        lo = mid + 1;
      } else {
        hi = mid;
      }
    }
    return lo;
  }

  private findIndex(score: bigint, userId: U): number {
    const index = this.lowerBound(score, userId);
    const entry = this.sorted[index];
    if (
      entry !== undefined &&
      entry.score === score &&
      this.compareUserIds(entry.userId, userId) === 0
    ) {
      return index;
    }
    return -1;
  }

  private computeTopK(): Array<RankedEntry<U>> {
    const limit = Math.min(this.k, this.sorted.length);
    const result: Array<RankedEntry<U>> = [];
    for (let i = 0; i < limit; i++) {
      result.push({ rank: i + 1, userId: this.sorted[i].userId, score: this.sorted[i].score });
    }
    return result;
  }

  private computeDiff(
    prev: ReadonlyArray<RankedEntry<U>>,
    next: ReadonlyArray<RankedEntry<U>>,
  ): TopKDiff<U> {
    const prevRank = new Map<U, number>();
    for (const entry of prev) prevRank.set(entry.userId, entry.rank);
    const nextRank = new Map<U, number>();
    for (const entry of next) nextRank.set(entry.userId, entry.rank);

    const entered = next.filter((entry) => !prevRank.has(entry.userId));
    const left = prev.filter((entry) => !nextRank.has(entry.userId));
    const moved: Array<MovedEntry<U>> = [];
    for (const entry of next) {
      const from = prevRank.get(entry.userId);
      if (from !== undefined && from !== entry.rank) {
        moved.push({ userId: entry.userId, from, to: entry.rank, score: entry.score });
      }
    }
    return { entered, left, moved };
  }
}
