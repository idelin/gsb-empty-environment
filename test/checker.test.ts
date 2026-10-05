import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkTrace } from '../src/checker.ts';
import { TraceConflictError, TraceValidationError } from '../src/trace.ts';
import {
  MEMBERS,
  legalNotificationDelayTrace,
  doubleChosenTrace,
  lowBallotAcceptTrace,
  restartLostStateTrace,
  outOfOrderApplyTrace,
  unquorumedLearnTrace,
  applyValueMismatchTrace,
  applyNotChosenTrace,
  missingLogsTrace,
} from './samples.ts';

test('legal trace with delayed notification: no violations, no incomplete', () => {
  const result = checkTrace(legalNotificationDelayTrace(), MEMBERS);
  assert.equal(result.ok, true);
  assert.deepEqual(result.violations, []);
  assert.deepEqual(result.incomplete, []);
  assert.equal(result.stats.events, 18);
});

test('double chosen: conflicting quorums for one slot are flagged', () => {
  const result = checkTrace(doubleChosenTrace(), MEMBERS);
  assert.equal(result.ok, false);
  assert.equal(result.violations.length, 1);
  const v = result.violations[0];
  assert.equal(v.kind, 'ConflictingChosen');
  assert.equal(v.evidence?.minimalProven, true);
  assert.equal(v.evidence?.eventIds.length, 8); // 2 learns + 2 quorums of 3
});

test('low ballot accept: accept below persisted promise is flagged', () => {
  const result = checkTrace(lowBallotAcceptTrace(), MEMBERS);
  assert.equal(result.ok, false);
  assert.equal(result.violations.length, 1);
  const v = result.violations[0];
  assert.equal(v.kind, 'AcceptBelowPersistedPromise');
  assert.deepEqual(v.evidence?.eventIds, ['a1', 'pe1']);
});

test('restart loses confirmed durable state', () => {
  const result = checkTrace(restartLostStateTrace(), MEMBERS);
  assert.equal(result.ok, false);
  assert.equal(result.violations.length, 1);
  const v = result.violations[0];
  assert.equal(v.kind, 'RestartLostPersistedState');
  assert.deepEqual(v.evidence?.eventIds, ['pe1', 'r1']);
});

test('out-of-order apply: slot 2 before slot 1', () => {
  const result = checkTrace(outOfOrderApplyTrace(), MEMBERS);
  assert.equal(result.ok, false);
  assert.equal(result.violations.length, 1);
  const v = result.violations[0];
  assert.equal(v.kind, 'ApplyGap');
  assert.deepEqual(v.evidence?.eventIds, ['ap1']);
});

test('learn without a quorum of distinct members', () => {
  const result = checkTrace(unquorumedLearnTrace(), MEMBERS);
  assert.equal(result.ok, false);
  assert.equal(result.violations.length, 1);
  const v = result.violations[0];
  assert.equal(v.kind, 'UnquorumedLearn');
  assert.deepEqual(v.evidence?.eventIds, ['a1', 'a2', 'l1']);
});

test('apply with mismatched learn value', () => {
  const result = checkTrace(applyValueMismatchTrace(), MEMBERS);
  assert.equal(result.ok, false);
  const kinds = result.violations.map((v) => v.kind);
  assert.deepEqual(kinds, ['ApplyValueMismatch']);
  assert.deepEqual(result.violations[0].evidence?.eventIds, ['ap1', 'l1']);
});

test('apply of a value that was never chosen', () => {
  const result = checkTrace(applyNotChosenTrace(), MEMBERS);
  assert.equal(result.ok, false);
  const kinds = result.violations.map((v) => v.kind).sort();
  assert.deepEqual(kinds, ['ApplyNotChosen', 'UnquorumedLearn']);
});

test('missing logs: incomplete findings, never violations', () => {
  const result = checkTrace(missingLogsTrace(), MEMBERS);
  assert.equal(result.ok, true, 'missing logs must not convict the implementation');
  assert.deepEqual(result.violations, []);
  assert.equal(result.incomplete.length, 2);
  const byKind = new Map(result.incomplete.map((f) => [f.kind, f]));
  assert.deepEqual(byKind.get('UnquorumedLearn')?.missingCauses, ['a3']);
  assert.deepEqual(byKind.get('ApplyNotChosen')?.missingCauses, ['l9']);
});

test('duplicate event id with identical content is idempotent', () => {
  const trace = legalNotificationDelayTrace();
  const withDup = [...trace, trace[3], trace[7]];
  const result = checkTrace(withDup, MEMBERS);
  assert.equal(result.ok, true);
  assert.equal(result.stats.duplicatesIgnored, 2);
  assert.equal(result.stats.events, 18);
});

test('duplicate event id with conflicting content rejects the trace', () => {
  const trace = legalNotificationDelayTrace();
  const tampered = { ...trace[0], ballot: { round: 9, node: 'n9' } };
  assert.throws(() => checkTrace([...trace, tampered], MEMBERS), TraceConflictError);
});

test('cluster must have exactly five members', () => {
  assert.throws(() => checkTrace([], ['n1', 'n2', 'n3', 'n4']), TraceValidationError);
  assert.throws(() => checkTrace([], ['n1', 'n1', 'n2', 'n3', 'n4']), TraceValidationError);
});

test('events from non-member nodes are rejected', () => {
  const trace = legalNotificationDelayTrace();
  trace[0] = { ...trace[0], node: 'n9' };
  assert.throws(() => checkTrace(trace, MEMBERS), TraceValidationError);
});

test('idempotent re-apply of the same slot and value is legal', () => {
  const trace = legalNotificationDelayTrace();
  const reapply = { ...trace[8], id: 'ap1-again' }; // apply n1 slot 1 "va" again
  const result = checkTrace([...trace, reapply], MEMBERS);
  assert.equal(result.ok, true);
  assert.deepEqual(result.violations, []);
});
