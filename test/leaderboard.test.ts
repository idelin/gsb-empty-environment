import test from 'node:test';
import assert from 'node:assert/strict';
import {
  Leaderboard,
  computeTotalsFromHistory,
  rankTotals,
  toBigInt,
  type LeaderboardEvent,
  type TopKDiff,
} from '../src/leaderboard.ts';

function score(id: string, userId: string, delta: bigint | number | string): LeaderboardEvent {
  return { type: 'score', id, userId, delta };
}

function revoke(targetId: string): LeaderboardEvent {
  return { type: 'revoke', targetId };
}

function applyAll(board: Leaderboard, events: LeaderboardEvent[]): TopKDiff[] {
  return events.map((event) => board.apply(event));
}

function topPairs(board: Leaderboard): Array<[string, bigint]> {
  return board.topK.map((entry) => [entry.userId, entry.score]);
}

test('ranks by total desc, user id asc on ties, and reports top-k diffs', () => {
  const board = new Leaderboard({ k: 2 });

  let diff = board.apply(score('e1', 'alice', 10));
  assert.deepEqual(diff.added.map((e) => [e.rank, e.userId]), [[1, 'alice']]);
  assert.equal(diff.removed.length, 0);
  assert.equal(diff.moved.length, 0);

  diff = board.apply(score('e2', 'bob', 10));
  // tie at 10: alice < bob, alice keeps rank 1
  assert.deepEqual(topPairs(board), [
    ['alice', 10n],
    ['bob', 10n],
  ]);
  assert.deepEqual(diff.added.map((e) => [e.rank, e.userId]), [[2, 'bob']]);

  diff = board.apply(score('e3', 'bob', 5));
  assert.deepEqual(topPairs(board), [
    ['bob', 15n],
    ['alice', 10n],
  ]);
  assert.deepEqual(diff.moved, [
    { userId: 'bob', from: 2, to: 1, score: 15n },
    { userId: 'alice', from: 1, to: 2, score: 10n },
  ]);

  diff = board.apply(score('e4', 'carol', 20));
  // carol enters at 1, alice pushed out of top-2
  assert.deepEqual(topPairs(board), [
    ['carol', 20n],
    ['bob', 15n],
  ]);
  assert.deepEqual(diff.added.map((e) => e.userId), ['carol']);
  assert.deepEqual(diff.removed.map((e) => [e.rank, e.userId]), [[2, 'alice']]);
  assert.deepEqual(diff.moved, [{ userId: 'bob', from: 1, to: 2, score: 15n }]);
});

test('revoking a known event removes its contribution', () => {
  const board = new Leaderboard({ k: 3 });
  applyAll(board, [score('e1', 'alice', 10), score('e2', 'bob', 7)]);

  const diff = board.apply(revoke('e1'));
  assert.equal(board.totalOf('alice'), 0n);
  assert.equal(board.isRevoked('e1'), true);
  assert.deepEqual(topPairs(board), [
    ['bob', 7n],
    ['alice', 0n],
  ]);
  assert.deepEqual(diff.moved, [
    { userId: 'bob', from: 2, to: 1, score: 7n },
    { userId: 'alice', from: 1, to: 2, score: 0n },
  ]);
});

test('revocation of an unknown id is held pending and the late event never takes effect', () => {
  const board = new Leaderboard({ k: 2 });

  let diff = board.apply(revoke('ghost'));
  assert.equal(board.pendingRevocationCount, 1);
  assert.equal(board.isRevoked('ghost'), true);
  assert.deepEqual(diff, { added: [], removed: [], moved: [] });

  diff = board.apply(score('e1', 'alice', 5));
  assert.deepEqual(topPairs(board), [['alice', 5n]]);

  // the revoked-before-arrival event arrives late: stillborn, no diff, no total
  diff = board.apply(score('ghost', 'mallory', 1000));
  assert.deepEqual(diff, { added: [], removed: [], moved: [] });
  assert.equal(board.totalOf('mallory'), undefined);
  assert.deepEqual(topPairs(board), [['alice', 5n]]);
});

test('duplicate revocations are no-ops', () => {
  const board = new Leaderboard({ k: 2 });
  applyAll(board, [score('e1', 'alice', 10), revoke('e1')]);
  assert.equal(board.totalOf('alice'), 0n);

  const diff = board.apply(revoke('e1'));
  assert.deepEqual(diff, { added: [], removed: [], moved: [] });
  assert.equal(board.totalOf('alice'), 0n);

  // duplicate pending revocation is also idempotent
  board.apply(revoke('ghost'));
  board.apply(revoke('ghost'));
  assert.equal(board.pendingRevocationCount, 1);
});

test('duplicate score event ids are applied at most once', () => {
  const board = new Leaderboard({ k: 2 });
  board.apply(score('e1', 'alice', 10));
  const diff = board.apply(score('e1', 'alice', 10));
  assert.deepEqual(diff, { added: [], removed: [], moved: [] });
  assert.equal(board.totalOf('alice'), 10n);

  // a revoked event re-sent later stays revoked
  board.apply(revoke('e1'));
  board.apply(score('e1', 'alice', 10));
  assert.equal(board.totalOf('alice'), 0n);
});

test('ranking across zero: negative totals rank below zero, zero stays on the board', () => {
  const board = new Leaderboard({ k: 4 });
  applyAll(board, [
    score('e1', 'alice', 5),
    score('e2', 'bob', -3),
    score('e3', 'carol', 0),
    score('e4', 'dan', -10),
  ]);
  assert.deepEqual(topPairs(board), [
    ['alice', 5n],
    ['carol', 0n],
    ['bob', -3n],
    ['dan', -10n],
  ]);

  // alice crosses zero downwards past carol and ties bob at -3
  const diff = board.apply(score('e5', 'alice', -8));
  assert.equal(board.totalOf('alice'), -3n);
  assert.deepEqual(topPairs(board), [
    ['carol', 0n],
    ['alice', -3n],
    ['bob', -3n],
    ['dan', -10n],
  ]);
  // tie at -3: alice < bob
  assert.deepEqual(diff.moved, [
    { userId: 'carol', from: 2, to: 1, score: 0n },
    { userId: 'alice', from: 1, to: 2, score: -3n },
  ]);

  // revoking a negative delta raises the total back across zero
  board.apply(revoke('e4'));
  assert.equal(board.totalOf('dan'), 0n);
  assert.deepEqual(topPairs(board), [
    ['carol', 0n],
    ['dan', 0n],
    ['alice', -3n],
    ['bob', -3n],
  ]);
});

test('exact integer totals beyond 2^53', () => {
  const board = new Leaderboard({ k: 2 });
  const big1 = '9007199254740993'; // 2^53 + 1, not representable as a double
  const big2 = 9007199254740992n; // 2^53
  applyAll(board, [score('e1', 'alice', big1), score('e2', 'bob', big2)]);
  assert.equal(board.totalOf('alice'), 9007199254740993n);
  assert.deepEqual(topPairs(board), [
    ['alice', 9007199254740993n],
    ['bob', 9007199254740992n],
  ]);
  board.apply(revoke('e1'));
  assert.equal(board.totalOf('alice'), 0n);
});

test('rejects non-integer deltas', () => {
  const board = new Leaderboard({ k: 1 });
  assert.throws(() => board.apply(score('e1', 'alice', 1.5)), TypeError);
  assert.throws(() => board.apply(score('e2', 'alice', 'abc')), TypeError);
  assert.equal(toBigInt('-42'), -42n);
});

test('scripted history matches full replay at every step', () => {
  const board = new Leaderboard({ k: 3 });
  const events: LeaderboardEvent[] = [
    revoke('late1'), // revoke before arrival
    score('e1', 'alice', 10),
    score('e2', 'bob', -4),
    revoke('e1'),
    revoke('e1'), // duplicate revocation
    score('late1', 'carol', 50), // stillborn
    score('e3', 'alice', 3),
    score('e2', 'bob', -4), // duplicate event id
    revoke('e3'),
    score('e4', 'dan', 0),
  ];
  for (const event of events) {
    board.apply(event);
    board.verifyAgainstReplay();
  }
  assert.deepEqual(topPairs(board), [
    ['alice', 0n],
    ['dan', 0n],
    ['bob', -4n],
  ]);
});

test('fuzz: incremental state matches full-history replay after every event', () => {
  let seed = 0x2f6e2b1;
  const random = (): number => {
    // xorshift32, deterministic
    seed ^= seed << 13;
    seed ^= seed >>> 17;
    seed ^= seed << 5;
    return (seed >>> 0) / 0x100000000;
  };

  for (let round = 0; round < 20; round += 1) {
    const k = 1 + Math.floor(random() * 5);
    const board = new Leaderboard({ k });
    const users = ['u1', 'u2', 'u3', 'u4', 'u5', 'u6'];
    const issuedIds: string[] = [];
    let nextId = 0;

    for (let step = 0; step < 200; step += 1) {
      const roll = random();
      let event: LeaderboardEvent;
      if (roll < 0.55 || issuedIds.length === 0) {
        const id = `ev${nextId}`;
        nextId += 1;
        issuedIds.push(id);
        const userId = users[Math.floor(random() * users.length)]!;
        const delta = BigInt(Math.floor(random() * 41) - 20); // -20..20, crosses zero
        event = score(id, userId, delta);
      } else if (roll < 0.8) {
        // revoke a known event (possibly already revoked)
        const targetId = issuedIds[Math.floor(random() * issuedIds.length)]!;
        event = revoke(targetId);
      } else if (roll < 0.9) {
        // revoke an unknown id (may or may not arrive later)
        event = revoke(`ev${nextId + Math.floor(random() * 3)}`);
      } else {
        // duplicate an old score event
        const targetId = issuedIds[Math.floor(random() * issuedIds.length)]!;
        const userId = users[Math.floor(random() * users.length)]!;
        event = score(targetId, userId, 7);
      }
      board.apply(event);
      board.verifyAgainstReplay();

      // independent cross-check of the top-k itself
      const replayed = rankTotals(computeTotalsFromHistory(board.history)).slice(0, k);
      assert.deepEqual(
        board.topK.map((e) => [e.rank, e.userId, e.score]),
        replayed.map((e) => [e.rank, e.userId, e.score]),
      );
    }
  }
});
