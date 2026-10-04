import assert from 'node:assert/strict';
import { checkTrace, samples } from '../dist/index.js';

const kinds = (report) => report.violations.map((v) => v.kind);

export function testDoubleChosen() {
  const report = checkTrace(samples.doubleChosen());
  assert.equal(report.status, 'violations');
  assert.deepEqual(kinds(report), ['conflicting_chosen']);
  const v = report.violations[0];
  assert.equal(v.evidence.minimal, true);
  assert.equal(v.evidence.verified, true);
  assert.equal(v.evidence.eventIds.length, 8);
}

export function testLowBallotAccept() {
  const report = checkTrace(samples.lowBallotAccept());
  assert.equal(report.status, 'violations');
  assert.deepEqual(kinds(report), ['accept_below_promise']);
  assert.deepEqual(report.violations[0].evidence.eventIds, ['a1', 'pe1']);
}

export function testRestartStateLoss() {
  const report = checkTrace(samples.restartStateLoss());
  assert.equal(report.status, 'violations');
  assert.deepEqual(kinds(report), ['restart_state_loss']);
  assert.deepEqual(report.violations[0].evidence.eventIds, ['a1', 'pe1', 'r1']);
}

export function testOutOfOrderApply() {
  const report = checkTrace(samples.outOfOrderApply());
  assert.equal(report.status, 'violations');
  assert.deepEqual(kinds(report), ['apply_out_of_order', 'apply_out_of_order']);
}

export function testLegalNotificationDelay() {
  const report = checkTrace(samples.legalNotificationDelay());
  assert.equal(report.status, 'ok');
  assert.equal(report.violations.length, 0);
  assert.equal(report.incomplete.length, 0);
}

export function testMissingLogsIsIncompleteNotViolation() {
  const report = checkTrace(samples.missingLogs());
  assert.equal(report.status, 'incomplete');
  assert.equal(report.violations.length, 0);
  const notes = report.incomplete.filter((n) => n.eventId === 'l1');
  assert.ok(notes.length >= 1);
  assert.ok(notes.some((n) => n.missing.includes('a3')));
}

export function testDuplicateIdempotent() {
  const e = { id: 'a1', kind: 'accept', node: 'n1', epoch: 1, ballot: 1, slot: 1, value: 'v:A', causes: [] };
  const report = checkTrace([e, { ...e, causes: [] }]);
  assert.equal(report.status, 'ok');
  assert.equal(report.eventCount, 1);
  assert.equal(report.duplicateCount, 1);
}

export function testConflictingDuplicateRejected() {
  const a = { id: 'a1', kind: 'accept', node: 'n1', epoch: 1, ballot: 1, slot: 1, value: 'v:A', causes: [] };
  const b = { ...a, ballot: 2 };
  const report = checkTrace([a, b]);
  assert.equal(report.status, 'rejected');
  assert.equal(report.conflicts.length, 1);
  assert.equal(report.conflicts[0].id, 'a1');
}

export function testLearnWithoutQuorum() {
  const report = checkTrace([
    { id: 'a1', kind: 'accept', node: 'n1', epoch: 1, ballot: 1, slot: 1, value: 'v:A', causes: [] },
    { id: 'a2', kind: 'accept', node: 'n2', epoch: 1, ballot: 1, slot: 1, value: 'v:A', causes: [] },
    { id: 'l1', kind: 'learn', node: 'n1', epoch: 1, ballot: 1, slot: 1, value: 'v:A', causes: ['a1', 'a2'] },
  ]);
  assert.equal(report.status, 'violations');
  assert.deepEqual(kinds(report), ['learn_without_quorum']);
  assert.deepEqual(report.violations[0].evidence.eventIds, ['a1', 'a2', 'l1']);
}

export function testApplyWithoutChosen() {
  const report = checkTrace([
    { id: 'ap1', kind: 'apply', node: 'n1', epoch: 1, ballot: 0, slot: 1, value: 'v:A', causes: [] },
  ]);
  assert.equal(report.status, 'violations');
  assert.deepEqual(kinds(report), ['apply_without_chosen']);
  assert.deepEqual(report.violations[0].evidence.eventIds, ['ap1']);
}

export function testInvalidBallot() {
  const report = checkTrace([
    { id: 'a1', kind: 'accept', node: 'n1', epoch: 1, ballot: 1.5, slot: 1, value: 'v:A', causes: [] },
  ]);
  assert.equal(report.status, 'violations');
  assert.deepEqual(kinds(report), ['invalid_ballot']);
}

export function testUnknownMemberRejected() {
  const report = checkTrace([
    { id: 'a1', kind: 'accept', node: 'n9', epoch: 1, ballot: 1, slot: 1, value: 'v:A', causes: [] },
  ]);
  assert.equal(report.status, 'rejected');
  assert.ok(report.errors[0].includes('n9'));
}

export function testPromiseAfterRestart() {
  const report = checkTrace([
    { id: 'pe1', kind: 'persist', node: 'n1', epoch: 1, ballot: 5, slot: 0, value: null, causes: [] },
    { id: 'r1', kind: 'restart', node: 'n1', epoch: 2, ballot: 0, slot: 0, value: null, causes: ['pe1'] },
    { id: 'p1', kind: 'promise', node: 'n1', epoch: 2, ballot: 3, slot: 0, value: null, causes: ['r1'] },
  ]);
  assert.equal(report.status, 'violations');
  assert.deepEqual(kinds(report), ['restart_state_loss']);
}

export function testPersistedValueConflictAfterRestart() {
  const report = checkTrace([
    { id: 'pe1', kind: 'persist', node: 'n1', epoch: 1, ballot: 2, slot: 1, value: 'v:A', causes: [] },
    { id: 'r1', kind: 'restart', node: 'n1', epoch: 2, ballot: 0, slot: 0, value: null, causes: ['pe1'] },
    { id: 'a1', kind: 'accept', node: 'n1', epoch: 2, ballot: 3, slot: 1, value: 'v:B', causes: ['r1'] },
  ]);
  assert.equal(report.status, 'violations');
  assert.deepEqual(kinds(report), ['restart_state_loss']);
}

export function testHigherBallotAfterPersistIsLegal() {
  const report = checkTrace([
    { id: 'pe1', kind: 'persist', node: 'n1', epoch: 1, ballot: 5, slot: 0, value: null, causes: [] },
    { id: 'r1', kind: 'restart', node: 'n1', epoch: 2, ballot: 0, slot: 0, value: null, causes: ['pe1'] },
    { id: 'a1', kind: 'accept', node: 'n1', epoch: 2, ballot: 6, slot: 1, value: 'v:A', causes: ['r1'] },
  ]);
  assert.equal(report.status, 'ok');
}

export function testMalformedEventRejected() {
  const report = checkTrace([{ id: 'x', kind: 'accept' }]);
  assert.equal(report.status, 'rejected');
  assert.ok(report.errors.length > 0);
}

export function testNonArrayTraceRejected() {
  const report = checkTrace({ nope: true });
  assert.equal(report.status, 'rejected');
}
