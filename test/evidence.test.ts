import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkTrace } from '../src/checker.ts';
import { combinations } from '../src/evidence.ts';
import { verifyEvidence } from '../src/verifier.ts';
import type { TraceEvent, ViolationClaim } from '../src/types.ts';
import {
  MEMBERS,
  doubleChosenTrace,
  doubleChosenTieBreakTrace,
  lowBallotAcceptTrace,
  restartLostStateTrace,
  outOfOrderApplyTrace,
  unquorumedLearnTrace,
  applyValueMismatchTrace,
  applyNotChosenTrace,
} from './samples.ts';

/** Brute-force: the first subset of the whole trace, in (size, lexicographic)
 *  order, that the independent verifier accepts. */
function bruteForceMinimal(events: TraceEvent[], claim: ViolationClaim): string[] | null {
  const ids = events.map((e) => e.id).sort();
  for (let size = 1; size <= ids.length; size++) {
    for (const combo of combinations(ids, size)) {
      const keep = new Set(combo);
      const subset = events.filter((e) => keep.has(e.id));
      if (verifyEvidence(subset, claim)) return combo;
    }
  }
  return null;
}

const violationSamples: Array<[string, () => TraceEvent[]]> = [
  ['doubleChosen', doubleChosenTrace],
  ['doubleChosenTieBreak', doubleChosenTieBreakTrace],
  ['lowBallotAccept', lowBallotAcceptTrace],
  ['restartLostState', restartLostStateTrace],
  ['outOfOrderApply', outOfOrderApplyTrace],
  ['unquorumedLearn', unquorumedLearnTrace],
  ['applyValueMismatch', applyValueMismatchTrace],
  ['applyNotChosen', applyNotChosenTrace],
];

for (const [name, build] of violationSamples) {
  test(`minimal evidence matches full subset enumeration: ${name}`, () => {
    const trace = build();
    const result = checkTrace(trace, MEMBERS);
    assert.ok(result.violations.length > 0, 'sample must produce violations');
    for (const finding of result.violations) {
      const evidence = finding.evidence;
      assert.ok(evidence, 'violation must carry evidence');
      assert.equal(evidence.minimalProven, true);
      // The evidence itself must verify against the independent verifier.
      assert.equal(verifyEvidence(evidence.events, evidence.claim), true);
      // And it must be the global minimum over the whole trace.
      const expected = bruteForceMinimal(trace, evidence.claim);
      assert.deepEqual(
        evidence.eventIds,
        expected,
        `${name}/${finding.kind}: checker evidence must equal brute-force minimum`,
      );
    }
  });
}

test('lexicographic tie-break: smallest id list wins among equal-size minima', () => {
  const result = checkTrace(doubleChosenTieBreakTrace(), MEMBERS);
  const v = result.violations.find((x) => x.kind === 'ConflictingChosen');
  assert.ok(v?.evidence);
  // 3 of the 4 "va" accepts suffice; the lexicographically smallest triple wins.
  assert.deepEqual(v.evidence.eventIds, ['l1', 'l2', 'q1', 'q2', 'q3', 'r1', 'r2', 'r3']);
});

test('budget exhaustion: valid counterexample with not-proven-minimal flag', () => {
  const result = checkTrace(doubleChosenTrace(), MEMBERS, { budget: 1 });
  const v = result.violations[0];
  assert.ok(v.evidence);
  assert.equal(v.evidence.minimalProven, false);
  assert.equal(v.evidence.budgetExhausted, true);
  // The fallback is the full causal closure and it must still be a valid counterexample.
  assert.equal(v.evidence.eventIds.length, 8);
  assert.equal(verifyEvidence(v.evidence.events, v.evidence.claim), true);
});

test('tight but sufficient budget still proves minimality', () => {
  // lowBallotAccept closure is {p1, pe1, a1}; sizes 1..2 need 3 + 3 = 6 calls.
  const result = checkTrace(lowBallotAcceptTrace(), MEMBERS, { budget: 7 });
  const v = result.violations[0];
  assert.equal(v.evidence?.minimalProven, true);
  assert.deepEqual(v.evidence?.eventIds, ['a1', 'pe1']);
});

test('combinations generator yields lexicographic order', () => {
  const combos = [...combinations(['a', 'b', 'c', 'd'], 2)];
  assert.deepEqual(combos, [
    ['a', 'b'], ['a', 'c'], ['a', 'd'], ['b', 'c'], ['b', 'd'], ['c', 'd'],
  ]);
});
