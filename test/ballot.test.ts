import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ZERO_BALLOT, compareBallot, ballotEquals, formatBallot } from '../src/ballot.ts';

test('ballot ordering: round dominates, node id breaks ties', () => {
  const a = { round: 1, node: 'n1' };
  const b = { round: 2, node: 'n1' };
  const c = { round: 2, node: 'n2' };
  assert.ok(compareBallot(a, b) < 0);
  assert.ok(compareBallot(b, a) > 0);
  assert.ok(compareBallot(b, c) < 0, 'same round: lower node id loses');
  assert.equal(compareBallot(c, { round: 2, node: 'n2' }), 0);
  assert.ok(compareBallot(ZERO_BALLOT, a) < 0);
});

test('ballot equality and formatting', () => {
  assert.ok(ballotEquals({ round: 3, node: 'n5' }, { round: 3, node: 'n5' }));
  assert.ok(!ballotEquals({ round: 3, node: 'n5' }, { round: 3, node: 'n4' }));
  assert.ok(!ballotEquals({ round: 3, node: 'n5' }, { round: 4, node: 'n5' }));
  assert.equal(formatBallot({ round: 3, node: 'n5' }), '3@n5');
});
