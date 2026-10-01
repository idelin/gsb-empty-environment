import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  makeDropFrameRule,
  ntscDropFrame,
  compileRankIndex,
  floorDiv,
  floorMod,
} from '../src/dropframe.ts';

test('ntscDropFrame(30): classic period table', () => {
  const rule = ntscDropFrame(30);
  assert.equal(rule.periodFrames, 18000n); // 10 minutes at nominal 30 fps
  assert.equal(rule.drops.length, 18); // 2 labels x 9 minutes
  assert.equal(rule.validPerPeriod, 17982n);
  assert.deepEqual(rule.drops.slice(0, 4), [1800n, 1801n, 3600n, 3601n]);
  assert.deepEqual(rule.drops.slice(-2), [16200n, 16201n]);
  assert.throws(() => ntscDropFrame(25), RangeError);
});

test('ntscDropFrame(60): 59.94 drops 4 labels per minute', () => {
  const rule = ntscDropFrame(60);
  assert.equal(rule.drops.length, 36);
  assert.deepEqual(rule.drops.slice(0, 4), [3600n, 3601n, 3602n, 3603n]);
});

test('rank index: known NTSC landmarks', () => {
  const idx = compileRankIndex(ntscDropFrame(30));
  // rank -> nominal
  assert.equal(idx.rankToNominal(0n), 0n);
  assert.equal(idx.rankToNominal(1799n), 1799n); // 00:00:59;29
  assert.equal(idx.rankToNominal(1800n), 1802n); // 00:01:00;02 (1800/1801 dropped)
  assert.equal(idx.rankToNominal(17981n), 17999n); // 00:09:59;29
  assert.equal(idx.rankToNominal(17982n), 18000n); // 00:10:00;00 (no drop at x10 min)
  // nominal -> rank
  assert.equal(idx.nominalToRank(1802n), 1800n);
  assert.equal(idx.nominalToRank(18000n), 17982n);
  // negative side is symmetric
  assert.equal(idx.rankToNominal(-1n), -1n);
  assert.equal(idx.rankToNominal(-1800n), -1802n);
  assert.equal(idx.rankToNominal(-17982n), -18000n);
  assert.equal(idx.nominalToRank(-1802n), -1800n);
});

test('validity and nearest legal candidate', () => {
  const idx = compileRankIndex(ntscDropFrame(30));
  assert.equal(idx.isValidNominal(1800n), false);
  assert.equal(idx.isValidNominal(1801n), false);
  assert.equal(idx.isValidNominal(1802n), true);
  assert.equal(idx.isValidNominal(18000n), true);
  assert.equal(idx.isValidNominal(-1800n), false); // mirrors 16200 mod 18000
  assert.deepEqual(idx.nearestValidNominal(1800n), { nominal: 1799n, distance: -1n });
  assert.deepEqual(idx.nearestValidNominal(1801n), { nominal: 1802n, distance: 1n });
  assert.deepEqual(idx.nearestValidNominal(1802n), { nominal: 1802n, distance: 0n });
});

test('exhaustive round-trip across two full periods, both signs', () => {
  const idx = compileRankIndex(ntscDropFrame(30));
  const span = 2n * 17982n;
  for (let rank = -span; rank <= span; rank++) {
    assert.equal(idx.nominalToRank(idx.rankToNominal(rank)), rank, `rank ${rank}`);
  }
});

test('period boundaries: exhaustive neighborhood of every boundary 1..20', () => {
  const idx = compileRankIndex(ntscDropFrame(30));
  for (let k = 1n; k <= 20n; k++) {
    const boundary = k * 17982n;
    for (let d = -64n; d <= 64n; d++) {
      const rank = boundary + d;
      assert.equal(idx.nominalToRank(idx.rankToNominal(rank)), rank, `rank ${rank}`);
    }
    // nominal side: positions around k*18000 map back consistently
    const nominal = k * 18000n;
    assert.equal(idx.rankToNominal(idx.nominalToRank(nominal)), nominal);
  }
});

test('long durations use period jumps, not scans (huge ranks stay exact)', () => {
  const idx = compileRankIndex(ntscDropFrame(30));
  const rank = 10n ** 15n;
  assert.equal(idx.nominalToRank(idx.rankToNominal(rank)), rank);
  // exact closed form at period multiples: rank k*17982 <-> nominal k*18000
  const k = 10n ** 12n;
  assert.equal(idx.rankToNominal(k * 17982n), k * 18000n);
  assert.equal(idx.nominalToRank(k * 18000n), k * 17982n);
  // monotonicity across a huge stride
  assert.ok(idx.rankToNominal(rank) < idx.rankToNominal(rank + 1n));
});

test('custom period table: arbitrary drop patterns work generically', () => {
  const rule = makeDropFrameRule({
    id: 'odd',
    nominalFps: 25,
    periodFrames: 250,
    drops: [7, 50, 51, 52],
  });
  assert.equal(rule.validPerPeriod, 246n);
  const idx = compileRankIndex(rule);
  for (let rank = -2n * 246n; rank <= 2n * 246n; rank++) {
    assert.equal(idx.nominalToRank(idx.rankToNominal(rank)), rank, `rank ${rank}`);
  }
  // nearest candidate: 50 -> 49 (dist 1 vs 3); 51 -> tie(2,2) -> earlier label
  assert.deepEqual(idx.nearestValidNominal(50n), { nominal: 49n, distance: -1n });
  assert.deepEqual(idx.nearestValidNominal(51n), { nominal: 49n, distance: -2n });
  assert.deepEqual(idx.nearestValidNominal(52n), { nominal: 53n, distance: 1n });
});

test('rule validation rejects bad tables', () => {
  assert.throws(() => makeDropFrameRule({ id: 'x', nominalFps: 30, periodFrames: 10, drops: [10] }), RangeError);
  assert.throws(() => makeDropFrameRule({ id: 'x', nominalFps: 30, periodFrames: 10, drops: [3, 3] }), RangeError);
  assert.throws(() => makeDropFrameRule({ id: 'x', nominalFps: 30, periodFrames: 2, drops: [0, 1] }), RangeError);
  assert.throws(() => makeDropFrameRule({ id: 'x', nominalFps: 30, periodFrames: 0, drops: [] }), RangeError);
});

test('floorDiv/floorMod follow floored semantics for negatives', () => {
  assert.equal(floorDiv(-1n, 18000n), -1n);
  assert.equal(floorMod(-1n, 18000n), 17999n);
  assert.equal(floorDiv(-18000n, 18000n), -1n);
  assert.equal(floorMod(-18000n, 18000n), 0n);
  assert.equal(floorDiv(7n, 3n), 2n);
  assert.equal(floorMod(7n, 3n), 1n);
});
