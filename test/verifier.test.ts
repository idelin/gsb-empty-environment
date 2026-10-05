import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkTrace } from '../src/checker.ts';
import { verifyEvidence } from '../src/verifier.ts';
import type { TraceEvent, ViolationClaim } from '../src/types.ts';
import {
  MEMBERS,
  legalNotificationDelayTrace,
  doubleChosenTrace,
  lowBallotAcceptTrace,
  restartLostStateTrace,
  outOfOrderApplyTrace,
} from './samples.ts';

function evidenceOf(trace: TraceEvent[], kind: string) {
  const result = checkTrace(trace, MEMBERS);
  const finding = result.violations.find((v) => v.kind === kind);
  assert.ok(finding?.evidence, `expected a ${kind} violation`);
  return finding.evidence;
}

test('verifier accepts the minimal evidence for every violation kind', () => {
  const cases: Array<[TraceEvent[], string]> = [
    [doubleChosenTrace(), 'ConflictingChosen'],
    [lowBallotAcceptTrace(), 'AcceptBelowPersistedPromise'],
    [restartLostStateTrace(), 'RestartLostPersistedState'],
    [outOfOrderApplyTrace(), 'ApplyGap'],
  ];
  for (const [trace, kind] of cases) {
    const ev = evidenceOf(trace, kind);
    assert.equal(verifyEvidence(ev.events, ev.claim), true, kind);
  }
});

test('verifier rejects evidence with a dropped quorum accept', () => {
  const ev = evidenceOf(doubleChosenTrace(), 'ConflictingChosen');
  const reduced = ev.events.filter((e) => e.id !== 'a3');
  assert.equal(verifyEvidence(reduced, ev.claim), false);
});

test('verifier is order-sensitive: persist must precede the accept', () => {
  const ev = evidenceOf(lowBallotAcceptTrace(), 'AcceptBelowPersistedPromise');
  const reversed = [...ev.events].reverse();
  assert.equal(verifyEvidence(reversed, ev.claim), false);
});

test('verifier rejects empty evidence for every claim kind', () => {
  const claims: ViolationClaim[] = [
    { kind: 'AcceptBelowPersistedPromise', acceptId: 'a1' },
    { kind: 'RestartLostPersistedState', restartId: 'r1' },
    { kind: 'UnquorumedLearn', learnId: 'l1' },
    { kind: 'ConflictingChosen', slot: 1 },
    { kind: 'ApplyGap', applyId: 'ap1' },
    { kind: 'ApplyValueMismatch', applyId: 'ap1' },
    { kind: 'ApplyNotChosen', applyId: 'ap1' },
  ];
  for (const claim of claims) {
    assert.equal(verifyEvidence([], claim), false, claim.kind);
  }
});

test('verifier rejects UnquorumedLearn when causes are missing from evidence', () => {
  const trace: TraceEvent[] = [
    { id: 'a1', kind: 'accept', node: 'n1', epoch: 1, ballot: { round: 1, node: 'n1' }, slot: 1, value: 'va', causes: [] },
    { id: 'l1', kind: 'learn', node: 'n1', epoch: 1, ballot: { round: 1, node: 'n1' }, slot: 1, value: 'va', causes: ['a1', 'a2', 'a3'] },
  ];
  // a2/a3 absent: the missing logs might contain the quorum, so no proof.
  assert.equal(verifyEvidence(trace, { kind: 'UnquorumedLearn', learnId: 'l1' }), false);
});

test('legal trace yields no claims: verifier has nothing to prove', () => {
  const result = checkTrace(legalNotificationDelayTrace(), MEMBERS);
  assert.equal(result.ok, true);
  assert.equal(result.violations.length, 0);
});
