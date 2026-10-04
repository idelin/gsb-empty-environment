import assert from 'node:assert/strict';
import { checkTrace, verifyEvidence, samples } from '../dist/index.js';

const violationSamples = [
  ['doubleChosen', samples.doubleChosen],
  ['lowBallotAccept', samples.lowBallotAccept],
  ['restartStateLoss', samples.restartStateLoss],
  ['outOfOrderApply', samples.outOfOrderApply],
];

export function testVerifierAcceptsCheckerEvidence() {
  for (const [name, build] of violationSamples) {
    const report = checkTrace(build());
    assert.ok(report.violations.length > 0, name + ' should violate');
    for (const v of report.violations) {
      const res = verifyEvidence(v.evidence.events, v.claim);
      assert.ok(res.ok, name + ' / ' + v.kind + ': ' + res.reason);
    }
  }
}

export function testVerifierRejectsTamperedEvidence() {
  const report = checkTrace(samples.doubleChosen());
  const v = report.violations[0];
  // Drop one accept: one of the two quorums collapses.
  const tampered = v.evidence.events.filter((e) => e.id !== 'a3');
  const res = verifyEvidence(tampered, v.claim);
  assert.equal(res.ok, false);
}

export function testVerifierRejectsMissingFocus() {
  const report = checkTrace(samples.lowBallotAccept());
  const v = report.violations[0];
  const res = verifyEvidence(v.evidence.events.filter((e) => e.id !== 'pe1'), v.claim);
  assert.equal(res.ok, false);
}

export function testVerifierRejectsWrongClaimKind() {
  const report = checkTrace(samples.lowBallotAccept());
  const v = report.violations[0];
  const res = verifyEvidence(v.evidence.events, { ...v.claim, kind: 'conflicting_chosen' });
  assert.equal(res.ok, false);
}

export function testVerifierRejectsEmptyEvidence() {
  const report = checkTrace(samples.doubleChosen());
  const v = report.violations[0];
  const res = verifyEvidence([], v.claim);
  assert.equal(res.ok, false);
}

export function testVerifierClosednessForNegativeClaims() {
  // learn_without_quorum evidence must be causally closed: dropping a
  // referenced accept makes the evidence unverifiable rather than proving
  // anything about missing logs.
  const trace = [
    { id: 'a1', kind: 'accept', node: 'n1', epoch: 1, ballot: 1, slot: 1, value: 'v:A', causes: [] },
    { id: 'a2', kind: 'accept', node: 'n2', epoch: 1, ballot: 1, slot: 1, value: 'v:A', causes: [] },
    { id: 'l1', kind: 'learn', node: 'n1', epoch: 1, ballot: 1, slot: 1, value: 'v:A', causes: ['a1', 'a2'] },
  ];
  const report = checkTrace(trace);
  const v = report.violations[0];
  assert.equal(v.kind, 'learn_without_quorum');
  assert.ok(verifyEvidence(v.evidence.events, v.claim).ok);
  const open = v.evidence.events.filter((e) => e.id !== 'a2');
  assert.equal(verifyEvidence(open, v.claim).ok, false);
}
