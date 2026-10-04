import assert from 'node:assert/strict';
import { checkTrace, verifyEvidence, samples, compareIdLists } from '../dist/index.js';

function allSubsetsOrdered(ids) {
  const sorted = ids.slice().sort();
  const out = [];
  const n = sorted.length;
  for (let mask = 1; mask < (1 << n); mask++) {
    const combo = [];
    for (let i = 0; i < n; i++) if (mask & (1 << i)) combo.push(sorted[i]);
    out.push(combo);
  }
  out.sort((a, b) => a.length - b.length || compareIdLists(a, b));
  return out;
}

function bruteForceMinimal(events, closure, claim) {
  const byId = new Map(events.map((e) => [e.id, e]));
  for (const combo of allSubsetsOrdered(closure)) {
    if (!claim.focus.every((f) => combo.includes(f))) continue;
    const subset = combo.map((id) => byId.get(id));
    if (verifyEvidence(subset, claim).ok) return combo;
  }
  return null;
}

const traces = {
  doubleChosen: samples.doubleChosen(),
  lowBallotAccept: samples.lowBallotAccept(),
  restartStateLoss: samples.restartStateLoss(),
  outOfOrderApply: samples.outOfOrderApply(),
  learnWithoutQuorum: [
    { id: 'a1', kind: 'accept', node: 'n1', epoch: 1, ballot: 1, slot: 1, value: 'v:A', causes: [] },
    { id: 'a2', kind: 'accept', node: 'n2', epoch: 1, ballot: 1, slot: 1, value: 'v:A', causes: [] },
    { id: 'l1', kind: 'learn', node: 'n1', epoch: 1, ballot: 1, slot: 1, value: 'v:A', causes: ['a1', 'a2'] },
  ],
};

export function testEvidenceMatchesExhaustiveEnumeration() {
  for (const [name, trace] of Object.entries(traces)) {
    const report = checkTrace(trace);
    assert.ok(report.violations.length > 0, name);
    for (const v of report.violations) {
      assert.ok(v.closure.length <= 12, name + ' closure small enough to enumerate');
      const expected = bruteForceMinimal(trace, v.closure, v.claim);
      assert.deepEqual(v.evidence.eventIds, expected, name + ' / ' + v.kind);
      assert.equal(v.evidence.minimal, true, name + ' / ' + v.kind);
      assert.equal(v.evidence.budgetExhausted, false);
    }
  }
}

export function testBudgetExhaustionKeepsValidCounterexample() {
  const report = checkTrace(samples.doubleChosen(), { budget: 1 });
  const v = report.violations[0];
  assert.equal(v.evidence.minimal, false);
  assert.equal(v.evidence.budgetExhausted, true);
  // The reported counterexample is the full causal closure and still verifies.
  assert.deepEqual(v.evidence.eventIds, v.closure);
  assert.ok(verifyEvidence(v.evidence.events, v.claim).ok);
  assert.equal(v.evidence.verified, true);
}

export function testZeroBudgetStillReturnsValidCounterexample() {
  const report = checkTrace(samples.lowBallotAccept(), { budget: 0 });
  const v = report.violations[0];
  assert.equal(v.evidence.minimal, false);
  assert.ok(verifyEvidence(v.evidence.events, v.claim).ok);
}
