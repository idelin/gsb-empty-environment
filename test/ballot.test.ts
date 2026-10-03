import test from 'node:test';
import assert from 'node:assert/strict';
import { ZERO_BALLOT, ballotEq, ballotKey, compareBallot } from '../src/types.ts';

test('ballot: round dominates the ordering', () => {
  assert.ok(compareBallot({ round: 2, nodeId: 0 }, { round: 1, nodeId: 4 }) > 0);
  assert.ok(compareBallot({ round: 1, nodeId: 4 }, { round: 2, nodeId: 0 }) < 0);
});

test('ballot: nodeId breaks ties within a round', () => {
  assert.ok(compareBallot({ round: 3, nodeId: 2 }, { round: 3, nodeId: 1 }) > 0);
  assert.ok(compareBallot({ round: 3, nodeId: 1 }, { round: 3, nodeId: 2 }) < 0);
});

test('ballot: equality and reflexivity', () => {
  const a = { round: 7, nodeId: 3 };
  assert.equal(compareBallot(a, { ...a }), 0);
  assert.ok(ballotEq(a, { round: 7, nodeId: 3 }));
  assert.ok(!ballotEq(a, { round: 7, nodeId: 4 }));
});

test('ballot: ZERO_BALLOT is the bottom element', () => {
  for (let round = 0; round < 3; round++) {
    for (let nodeId = 0; nodeId < 5; nodeId++) {
      assert.ok(compareBallot({ round, nodeId }, ZERO_BALLOT) > 0);
    }
  }
});

test('ballot: key encoding is injective and consistent with equality', () => {
  const keys = new Set<string>();
  for (let round = 0; round < 10; round++) {
    for (let nodeId = 0; nodeId < 5; nodeId++) {
      keys.add(ballotKey({ round, nodeId }));
    }
  }
  assert.equal(keys.size, 50);
  assert.equal(ballotKey({ round: 12, nodeId: 4 }), '12@4');
});
