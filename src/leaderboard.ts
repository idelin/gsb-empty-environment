/**
 * Exact-integer leaderboard with revocable score events.
 *
 * Semantics:
 * - A score event carries a unique id, a user id and an exact integer delta.
 * - A revoke event targets a score event id.
 *   - Known target: the target's contribution is removed immediately.
 *   - Unknown target: the revocation is held pending; when the original
 *     event arrives later it is stillborn and never takes effect.
 *   - Duplicate revocations of the same target are no-ops.
 * - Duplicate score event ids are no-ops (at-most-once application).
 * - Totals are exact integers (bigint), safe beyond 2^53.
 * - Ranking: total score descending, ties broken by user id ascending.
 * - Users remain on the board even at exactly 0 (zero is a valid score).
 */

export type UserId = string;

export type IntInput = bigint | number | string;

export interface ScoreEvent {
  readonly type: 'score';
  readonly id: string;
  readonly userId: UserId;
  readonly delta: IntInput;
}

export interface RevokeEvent {
  readonly type: 'revoke';
  /** Optional id of the revocation itself, kept in history only. */
  readonly id?: string;
  /** Id of the score event to revoke. */
  readonly targetId: string;
}

export type LeaderboardEvent = ScoreEvent | RevokeEvent;

export interface TopKEntry {
  readonly rank: number;
  readonly userId: UserId;
  readonly score: bigint;
}

export interface MovedEntry {
  readonly userId: UserId;
  readonly from: number;
  readonly to: number;
  readonly score: bigint;
}

export interface TopKDiff {
  /** Users newly inside the top-k. */
  readonly added: readonly TopKEntry[];
  /** Users that dropped out of the top-k (rank is their previous rank). */
  readonly removed: readonly TopKEntry[];
  /** Users still inside the top-k whose rank changed. */
  readonly moved: readonly MovedEntry[];
}

export interface LeaderboardOptions {
  readonly k: number;
  /** Ascending tie-break comparator for user ids. Defaults to code-unit order. */
  readonly compareUserIds?: (a: UserId, b: UserId) => number;
}

export function toBigInt(value: IntInput): bigint {
  if (typeof value === 'bigint') return value;
  if (typeof value === 'number') {
    if (!Number.isInteger(value)) {
      throw new TypeError(`score delta must be an exact integer, got ${value}`);
    }
    return BigInt(value);
  }
  if (!/^-?\d+$/.test(value)) {
    throw new TypeError(`score delta must be an exact integer, got "${value}"`);
  }
  return BigInt(value);
}

function defaultCompareUserIds(a: UserId, b: UserId): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

interface StoredScoreEvent {
  readonly userId: UserId;
  readonly delta: bigint;
  revoked: boolean;
}

const EMPTY_DIFF: TopKDiff = Object.freeze({
  added: Object.freeze([]),
  removed: Object.freeze([]),
  moved: Object.freeze([]),
});

export class Leaderboard {
  readonly #k: number;
  readonly #compareUserIds: (a: UserId, b: UserId) => number;

  /** Every score event ever received, keyed by event id. */
  readonly #events = new Map<string, StoredScoreEvent>();
  /** Targets revoked before their score event arrived. */
  readonly #pendingRevocations = new Set<string>();
  /** Current exact totals per user. */
  readonly #totals = new Map<UserId, bigint>();
  /** Full event history, in arrival order, for replay verification. */
  readonly #history: LeaderboardEvent[] = [];

  #topK: readonly TopKEntry[] = [];

  constructor(options: LeaderboardOptions) {
    if (!Number.isInteger(options.k) || options.k < 1) {
      throw new RangeError(`k must be a positive integer, got ${options.k}`);
    }
    this.#k = options.k;
    this.#compareUserIds = options.compareUserIds ?? defaultCompareUserIds;
  }

  get k(): number {
    return this.#k;
  }

  /** Number of revocation targets still waiting for their score event. */
  get pendingRevocationCount(): number {
    return this.#pendingRevocations.size;
  }

  /** Full event history in arrival order. */
  get history(): readonly LeaderboardEvent[] {
    return this.#history;
  }

  /** Current top-k snapshot, ranks 1..k. */
  get topK(): readonly TopKEntry[] {
    return this.#topK;
  }

  /** Exact total for a user, or undefined if the user has no score events. */
  totalOf(userId: UserId): bigint | undefined {
    return this.#totals.get(userId);
  }

  /** Whether a score event id is revoked (including stillborn events). */
  isRevoked(eventId: string): boolean {
    if (this.#pendingRevocations.has(eventId)) return true;
    return this.#events.get(eventId)?.revoked ?? false;
  }

  /**
   * Apply one event and return the resulting top-k diff.
   * No-op events (duplicates, redundant revocations) return an empty diff.
   */
  apply(event: LeaderboardEvent): TopKDiff {
    this.#history.push(event);
    const changed =
      event.type === 'score' ? this.#applyScore(event) : this.#applyRevoke(event);
    if (!changed) return EMPTY_DIFF;
    return this.#refreshTopK();
  }

  #applyScore(event: ScoreEvent): boolean {
    if (this.#events.has(event.id)) return false; // duplicate id: no-op
    const delta = toBigInt(event.delta);
    const stillborn = this.#pendingRevocations.has(event.id);
    this.#events.set(event.id, { userId: event.userId, delta, revoked: stillborn });
    if (stillborn) return false; // revoked before arrival: never takes effect
    this.#addToTotal(event.userId, delta);
    return true;
  }

  #applyRevoke(event: RevokeEvent): boolean {
    const target = this.#events.get(event.targetId);
    if (target === undefined) {
      // Unknown target: hold pending (idempotent for duplicates).
      this.#pendingRevocations.add(event.targetId);
      return false;
    }
    if (target.revoked) return false; // duplicate revocation: no-op
    target.revoked = true;
    this.#addToTotal(target.userId, -target.delta);
    return true;
  }

  #addToTotal(userId: UserId, delta: bigint): void {
    this.#totals.set(userId, (this.#totals.get(userId) ?? 0n) + delta);
  }

  #rankAll(): TopKEntry[] {
    return rankTotals(this.#totals, this.#compareUserIds);
  }

  #refreshTopK(): TopKDiff {
    const next = this.#rankAll().slice(0, this.#k);
    const diff = diffTopK(this.#topK, next);
    this.#topK = next;
    return diff;
  }

  /**
   * Rebuild state from the retained history and verify it matches the
   * incrementally maintained state. Returns the replayed top-k.
   * Throws if incremental state and full replay disagree.
   */
  verifyAgainstReplay(): readonly TopKEntry[] {
    const replayedTotals = computeTotalsFromHistory(this.#history);
    const replayedTopK = rankTotals(replayedTotals, this.#compareUserIds).slice(0, this.#k);
    if (!totalsEqual(replayedTotals, this.#totals)) {
      throw new Error('leaderboard state diverged from full-history replay (totals)');
    }
    if (!topKEqual(replayedTopK, this.#topK)) {
      throw new Error('leaderboard state diverged from full-history replay (top-k)');
    }
    return replayedTopK;
  }
}

/** Compute the diff between two consecutive top-k snapshots. */
export function diffTopK(
  previous: readonly TopKEntry[],
  next: readonly TopKEntry[],
): TopKDiff {
  const prevByUser = new Map(previous.map((entry) => [entry.userId, entry]));
  const nextByUser = new Map(next.map((entry) => [entry.userId, entry]));

  const added: TopKEntry[] = [];
  const moved: MovedEntry[] = [];
  for (const entry of next) {
    const before = prevByUser.get(entry.userId);
    if (before === undefined) {
      added.push(entry);
    } else if (before.rank !== entry.rank) {
      moved.push({ userId: entry.userId, from: before.rank, to: entry.rank, score: entry.score });
    }
  }
  const removed: TopKEntry[] = [];
  for (const entry of previous) {
    if (!nextByUser.has(entry.userId)) removed.push(entry);
  }
  return { added, removed, moved };
}

/**
 * Independent pure fold over an event history. Used to cross-check the
 * incremental Leaderboard state (full-history replay).
 */
export function computeTotalsFromHistory(
  history: readonly LeaderboardEvent[],
): Map<UserId, bigint> {
  const events = new Map<string, { userId: UserId; delta: bigint; revoked: boolean }>();
  const pending = new Set<string>();
  const totals = new Map<UserId, bigint>();
  const add = (userId: UserId, delta: bigint): void => {
    totals.set(userId, (totals.get(userId) ?? 0n) + delta);
  };
  for (const event of history) {
    if (event.type === 'score') {
      if (events.has(event.id)) continue;
      const delta = toBigInt(event.delta);
      const revoked = pending.has(event.id);
      events.set(event.id, { userId: event.userId, delta, revoked });
      if (!revoked) add(event.userId, delta);
    } else {
      const target = events.get(event.targetId);
      if (target === undefined) {
        pending.add(event.targetId);
      } else if (!target.revoked) {
        target.revoked = true;
        add(target.userId, -target.delta);
      }
    }
  }
  return totals;
}

/** Rank a totals map: score descending, user id ascending. */
export function rankTotals(
  totals: ReadonlyMap<UserId, bigint>,
  compareUserIds: (a: UserId, b: UserId) => number = defaultCompareUserIds,
): TopKEntry[] {
  const rows = [...totals.entries()].map(([userId, score]) => ({ rank: 0, userId, score }));
  rows.sort(
    (a, b) =>
      (a.score < b.score ? 1 : a.score > b.score ? -1 : 0) ||
      compareUserIds(a.userId, b.userId),
  );
  rows.forEach((row, index) => {
    row.rank = index + 1;
  });
  return rows;
}

function totalsEqual(a: ReadonlyMap<UserId, bigint>, b: ReadonlyMap<UserId, bigint>): boolean {
  if (a.size !== b.size) return false;
  for (const [userId, score] of a) {
    if (b.get(userId) !== score) return false;
  }
  return true;
}

function topKEqual(a: readonly TopKEntry[], b: readonly TopKEntry[]): boolean {
  if (a.length !== b.length) return false;
  return a.every((entry, index) => {
    const other = b[index];
    return (
      other !== undefined &&
      entry.userId === other.userId &&
      entry.rank === other.rank &&
      entry.score === other.score
    );
  });
}
