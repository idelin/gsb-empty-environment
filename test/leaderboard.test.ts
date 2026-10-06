import { strict as assert } from "assert";
import {
  Leaderboard,
  LeaderboardEvent,
  RankedEntry,
  TopKDiff,
} from "../src/index";

let passed = 0;
function test(name: string, fn: () => void): void {
  fn();
  passed++;
  console.log(`ok - ${name}`);
}

function score<U>(id: string, userId: U, delta: bigint): LeaderboardEvent<U> {
  return { kind: "score", id, userId, delta };
}

function revoke<U>(id: string): LeaderboardEvent<U> {
  return { kind: "revoke", id };
}

test("revoke of unknown id is pending; late original never takes effect", () => {
  const board = new Leaderboard<number>(3);
  const r1 = board.apply(revoke("e1"));
  assert.equal(r1.accepted, true);
  assert.equal(board.eventState("e1"), "revoked");

  const r2 = board.apply(score("e1", 1, 100n));
  assert.equal(r2.accepted, false);
  assert.equal(board.getScore(1), 0n);
  assert.deepEqual(board.snapshot(), []);
  assert.equal(board.eventState("e1"), "revoked");
});

test("pending revocation does not block other events of the same user", () => {
  const board = new Leaderboard<number>(3);
  board.apply(revoke("e1"));
  board.apply(score("e2", 1, 3n));
  board.apply(score("e1", 1, 100n));
  assert.equal(board.getScore(1), 3n);
  assert.deepEqual(
    board.snapshot(),
    [{ rank: 1, userId: 1, score: 3n }],
  );
});

test("duplicate revocation is a no-op", () => {
  const board = new Leaderboard<number>(3);
  board.apply(score("e1", 1, 10n));
  const first = board.apply(revoke("e1"));
  const second = board.apply(revoke("e1"));
  assert.equal(first.accepted, true);
  assert.equal(second.accepted, false);
  assert.deepEqual(second.diff, { entered: [], left: [], moved: [] });
  assert.equal(board.getScore(1), 0n);
});

test("revoke of a live event removes its delta", () => {
  const board = new Leaderboard<number>(3);
  board.apply(score("e1", 1, 10n));
  board.apply(score("e2", 1, 5n));
  board.apply(revoke("e1"));
  assert.equal(board.getScore(1), 5n);
  assert.equal(board.eventState("e1"), "revoked");
  assert.equal(board.eventState("e2"), "live");
  assert.equal(board.eventState("e3"), "unknown");
});

test("duplicate score event id is applied only once", () => {
  const board = new Leaderboard<number>(3);
  board.apply(score("e1", 1, 10n));
  const dup = board.apply(score("e1", 1, 10n));
  assert.equal(dup.accepted, false);
  assert.equal(board.getScore(1), 10n);
});

test("ranking crosses zero: negative and zero scores stay ranked", () => {
  const board = new Leaderboard<number>(10);
  board.apply(score("e1", 1, 5n));
  board.apply(score("e2", 2, -3n));
  board.apply(score("e3", 3, 0n));
  board.apply(score("e4", 4, -10n));
  assert.deepEqual(board.snapshot(), [
    { rank: 1, userId: 1, score: 5n },
    { rank: 2, userId: 3, score: 0n },
    { rank: 3, userId: 2, score: -3n },
    { rank: 4, userId: 4, score: -10n },
  ]);
});

test("user revoked back to zero keeps its place across the zero line", () => {
  const board = new Leaderboard<number>(10);
  board.apply(score("e1", 1, 7n));
  board.apply(score("e2", 2, -2n));
  board.apply(revoke("e1"));
  assert.deepEqual(board.snapshot(), [
    { rank: 1, userId: 1, score: 0n },
    { rank: 2, userId: 2, score: -2n },
  ]);
});

test("ties break by ascending user id (numeric)", () => {
  const board = new Leaderboard<number>(10);
  board.apply(score("e1", 10, 5n));
  board.apply(score("e2", 2, 5n));
  board.apply(score("e3", 1, 5n));
  assert.deepEqual(
    board.snapshot().map((entry) => entry.userId),
    [1, 2, 10],
  );
});

test("ties break by ascending user id (string)", () => {
  const board = new Leaderboard<string>(10);
  board.apply(score("e1", "bob", -1n));
  board.apply(score("e2", "alice", -1n));
  assert.deepEqual(
    board.snapshot().map((entry) => entry.userId),
    ["alice", "bob"],
  );
});

test("scores are exact beyond 2^53", () => {
  const board = new Leaderboard<number>(10);
  board.apply(score("e1", 1, 9007199254740993n)); // 2^53 + 1
  board.apply(score("e2", 1, 9007199254740993n));
  board.apply(score("e3", 2, 18014398509481984n)); // 2^54
  assert.equal(board.getScore(1), 18014398509481986n);
  assert.deepEqual(
    board.snapshot().map((entry) => entry.userId),
    [1, 2],
  );
  board.apply(revoke("e2"));
  assert.equal(board.getScore(1), 9007199254740993n);
});

test("top-k diff reports entered, left and moved", () => {
  const board = new Leaderboard<number>(2);

  const d1 = board.apply(score("e1", 1, 5n)).diff;
  assert.deepEqual(d1, {
    entered: [{ rank: 1, userId: 1, score: 5n }],
    left: [],
    moved: [],
  });

  const d2 = board.apply(score("e2", 2, 10n)).diff;
  assert.deepEqual(d2, {
    entered: [{ rank: 1, userId: 2, score: 10n }],
    left: [],
    moved: [{ userId: 1, from: 1, to: 2, score: 5n }],
  });

  const d3 = board.apply(score("e3", 3, 7n)).diff;
  assert.deepEqual(d3, {
    entered: [{ rank: 2, userId: 3, score: 7n }],
    left: [{ rank: 2, userId: 1, score: 5n }],
    moved: [],
  });

  const d4 = board.apply(revoke("e2")).diff;
  assert.deepEqual(d4, {
    entered: [{ rank: 2, userId: 1, score: 5n }],
    left: [{ rank: 1, userId: 2, score: 10n }],
    moved: [{ userId: 3, from: 2, to: 1, score: 7n }],
  });
});

test("no-op events produce empty diffs", () => {
  const board = new Leaderboard<number>(2);
  board.apply(score("e1", 1, 5n));
  const dup = board.apply(score("e1", 1, 5n));
  assert.deepEqual(dup.diff, { entered: [], left: [], moved: [] });
  const lateRevoked = board.apply(score("e2", 2, 9n));
  board.apply(revoke("e2"));
  assert.equal(lateRevoked.accepted, true);
});

test("k = 0 keeps an empty board", () => {
  const board = new Leaderboard<number>(0);
  board.apply(score("e1", 1, 5n));
  assert.deepEqual(board.getTopK(), []);
  assert.equal(board.getScore(1), 5n);
});

test("k larger than population ranks everyone", () => {
  const board = new Leaderboard<number>(100);
  board.apply(score("e1", 1, 1n));
  board.apply(score("e2", 2, 2n));
  assert.equal(board.getTopK().length, 2);
});

test("rankOf reflects 1-based position", () => {
  const board = new Leaderboard<number>(3);
  board.apply(score("e1", 1, 5n));
  board.apply(score("e2", 2, 9n));
  assert.equal(board.rankOf(2), 1);
  assert.equal(board.rankOf(1), 2);
  assert.equal(board.rankOf(99), null);
});

test("invalid k is rejected", () => {
  assert.throws(() => new Leaderboard<number>(-1), RangeError);
  assert.throws(() => new Leaderboard<number>(1.5), RangeError);
});

// ---------- full-history replay cross-checks ----------

function mulberry32(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

interface NaiveResult {
  totals: Map<number, bigint>;
}

function naiveReplay(events: ReadonlyArray<LeaderboardEvent<number>>): NaiveResult {
  const live = new Map<string, { userId: number; delta: bigint }>();
  const revoked = new Set<string>();
  const seenUsers = new Set<number>();
  for (const event of events) {
    if (event.kind === "score") {
      if (revoked.has(event.id) || live.has(event.id)) continue;
      live.set(event.id, { userId: event.userId, delta: event.delta });
      seenUsers.add(event.userId);
    } else {
      if (revoked.has(event.id)) continue;
      revoked.add(event.id);
      live.delete(event.id);
    }
  }
  const totals = new Map<number, bigint>();
  for (const userId of seenUsers) totals.set(userId, 0n);
  for (const { userId, delta } of live.values()) {
    totals.set(userId, (totals.get(userId) ?? 0n) + delta);
  }
  return { totals };
}

function expectedSnapshot(naive: NaiveResult): Array<RankedEntry<number>> {
  return [...naive.totals.entries()]
    .sort((a, b) => {
      if (a[1] !== b[1]) return a[1] > b[1] ? -1 : 1;
      return a[0] - b[0];
    })
    .map(([userId, score], index) => ({ rank: index + 1, userId, score }));
}

function applyDiffToShadow(
  shadow: Array<RankedEntry<number>>,
  diff: TopKDiff<number>,
): Array<RankedEntry<number>> {
  const leftUsers = new Set(diff.left.map((entry) => entry.userId));
  const movedByUser = new Map(diff.moved.map((entry) => [entry.userId, entry]));
  const next: Array<RankedEntry<number>> = [];
  for (const entry of shadow) {
    if (leftUsers.has(entry.userId)) continue;
    const moved = movedByUser.get(entry.userId);
    if (moved !== undefined) {
      next.push({ rank: moved.to, userId: entry.userId, score: moved.score });
    } else {
      next.push(entry);
    }
  }
  for (const entry of diff.entered) next.push({ ...entry });
  next.sort((a, b) => a.rank - b.rank);
  return next;
}

function randomDelta(rand: () => number): bigint {
  const roll = rand();
  if (roll < 0.7) return BigInt(Math.floor(rand() * 11) - 5);
  if (roll < 0.85) return 0n;
  const huge = (1n << 60n) + BigInt(Math.floor(rand() * 1000));
  return rand() < 0.5 ? huge : -huge;
}

test("fuzz: incremental board matches full-history replay and naive model", () => {
  const rounds = 150;
  for (let round = 0; round < rounds; round++) {
    const rand = mulberry32(round * 2654435761 + 1);
    const k = Math.floor(rand() * 7);
    const userCount = 2 + Math.floor(rand() * 7);
    const idCount = 5 + Math.floor(rand() * 15);
    const board = new Leaderboard<number>(k);
    const history: Array<LeaderboardEvent<number>> = [];
    let shadow: Array<RankedEntry<number>> = [];

    const steps = 60;
    for (let step = 0; step < steps; step++) {
      let event: LeaderboardEvent<number>;
      if (rand() < 0.55) {
        const id = `e${Math.floor(rand() * idCount)}`;
        const userId = 1 + Math.floor(rand() * userCount);
        event = score(id, userId, randomDelta(rand));
      } else {
        const id = `e${Math.floor(rand() * idCount)}`;
        event = revoke(id);
      }
      history.push(event);
      const result = board.apply(event);

      shadow = applyDiffToShadow(shadow, result.diff);
      assert.deepEqual(
        shadow.map((entry) => [entry.rank, entry.userId]),
        board.getTopK().map((entry) => [entry.rank, entry.userId]),
        `shadow top-k diverged at round ${round} step ${step}`,
      );

      const replayed = Leaderboard.replay(history, k);
      assert.deepEqual(
        board.snapshot(),
        replayed.snapshot(),
        `replay snapshot diverged at round ${round} step ${step}`,
      );
      assert.deepEqual(
        board.getTopK(),
        replayed.getTopK(),
        `replay top-k diverged at round ${round} step ${step}`,
      );

      const expected = expectedSnapshot(naiveReplay(history));
      assert.deepEqual(
        board.snapshot(),
        expected,
        `naive model diverged at round ${round} step ${step}`,
      );
    }
  }
});

test("fuzz: revocation-heavy streams with duplicate revokes", () => {
  const rand = mulberry32(123456789);
  const board = new Leaderboard<number>(4);
  const history: Array<LeaderboardEvent<number>> = [];
  for (let step = 0; step < 400; step++) {
    const id = `e${Math.floor(rand() * 12)}`;
    let event: LeaderboardEvent<number>;
    if (rand() < 0.4) {
      event = score(id, 1 + Math.floor(rand() * 5), randomDelta(rand));
    } else {
      event = revoke(id); // often unknown or already revoked
    }
    history.push(event);
    board.apply(event);
    if (step % 25 === 0) {
      const replayed = Leaderboard.replay(history, 4);
      assert.deepEqual(board.snapshot(), replayed.snapshot());
      assert.deepEqual(board.snapshot(), expectedSnapshot(naiveReplay(history)));
    }
  }
  assert.deepEqual(board.snapshot(), expectedSnapshot(naiveReplay(history)));
});

console.log(`\n${passed} tests passed`);
