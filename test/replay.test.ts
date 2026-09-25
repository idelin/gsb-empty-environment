import test from 'node:test';
import assert from 'node:assert/strict';
import { evaluate } from '../src/evaluate.ts';
import { createEvidenceRecord } from '../src/evidence.ts';
import type { EvidenceRecord } from '../src/evidence.ts';
import { verifyRecord } from '../src/replay.ts';
import { and, hashRule, inSet, intCmp, not, or, summarizePolicy } from '../src/rule.ts';
import type { Policy } from '../src/rule.ts';
import { sealAttribute } from '../src/snapshot.ts';
import type { AttributeSnapshot } from '../src/snapshot.ts';

const policy: Policy = {
  id: 'loan-eligibility',
  version: '1.0.0',
  rule: and(
    inSet('region', [1, 2, 3]),
    intCmp('age', 'ge', 18),
    or(intCmp('score', 'gt', 700), intCmp('deposit', 'ge', 5000)),
  ),
};

const baseSnapshot: AttributeSnapshot = { region: 2, age: 34, score: 712, deposit: 1200 };

function clone(record: EvidenceRecord): EvidenceRecord {
  return structuredClone(record);
}

test('round-trip: saved record verifies offline', () => {
  const record = createEvidenceRecord(policy, baseSnapshot, 'r1');
  const result = verifyRecord(record, policy);
  assert.deepEqual(result, { status: 'verified', conclusion: true });
});

test('live attribute corrections do not affect the historical verdict', () => {
  const live = { region: 2, age: 34, score: 712, deposit: 1200 };
  const record = createEvidenceRecord(policy, { ...live }, 'r2');

  live.score = 100; // real-time correction after the decision
  live.region = 9;

  const historical = verifyRecord(record, policy);
  assert.deepEqual(historical, { status: 'verified', conclusion: true });

  const fresh = createEvidenceRecord(policy, { ...live }, 'r2b');
  assert.deepEqual(verifyRecord(fresh, policy), { status: 'verified', conclusion: false });
  assert.deepEqual(verifyRecord(record, policy), { status: 'verified', conclusion: true });
});

test('tampered rule version is rejected at the summary check', () => {
  const record = createEvidenceRecord(policy, baseSnapshot, 'r3');
  const result = verifyRecord(record, { ...policy, version: '9.9.9' });
  assert.equal(result.status, 'rejected');
  assert.equal(result.status === 'rejected' && result.divergence.kind, 'rule-summary-mismatch');
});

test('same trace paired with a different rule summary is rejected', () => {
  const record = createEvidenceRecord(policy, baseSnapshot, 'r4');
  const other: Policy = { id: 'loan-eligibility', version: '1.0.1', rule: policy.rule };
  const forged = clone(record);
  forged.ruleSummary = summarizePolicy(other); // valid summary, wrong one
  const result = verifyRecord(forged, policy);
  assert.equal(result.status, 'rejected');
  assert.equal(result.status === 'rejected' && result.divergence.kind, 'rule-summary-mismatch');
});

test('deleting one read item is located at its exact index', () => {
  const record = createEvidenceRecord(policy, baseSnapshot, 'r5');
  const tampered = clone(record);
  const removed = tampered.trace.reads.splice(1, 1)[0];
  const result = verifyRecord(tampered, policy);
  assert.equal(result.status, 'rejected');
  if (result.status === 'rejected' && result.divergence.kind === 'read-set-mismatch') {
    assert.equal(result.divergence.index, 1);
    assert.deepEqual(result.divergence.expected, removed);
  } else {
    assert.fail(`expected read-set-mismatch, got ${JSON.stringify(result)}`);
  }
});

test('omitting actual read fields cannot replay successfully', () => {
  const record = createEvidenceRecord(policy, baseSnapshot, 'r6');
  const tampered = clone(record);
  tampered.trace.reads = [];
  const result = verifyRecord(tampered, policy);
  assert.equal(result.status, 'rejected');
  assert.equal(result.status === 'rejected' && result.divergence.kind, 'read-set-mismatch');
});

test('swapped short-circuit order is located at the first differing child', () => {
  const orPolicy: Policy = {
    id: 'p-or',
    version: '1.0.0',
    rule: or(intCmp('a', 'eq', 1), intCmp('b', 'eq', 2)),
  };
  const record = createEvidenceRecord(orPolicy, { a: 0, b: 2 }, 'r7');
  const tampered = clone(record);
  const root = tampered.trace.subproofs[tampered.trace.root];
  root.children.reverse();
  const result = verifyRecord(tampered, orPolicy);
  assert.equal(result.status, 'rejected');
  if (result.status === 'rejected' && result.divergence.kind === 'trace-mismatch') {
    assert.equal(result.divergence.path, '$');
    assert.equal(result.divergence.field, 'children[0]');
  } else {
    assert.fail(`expected trace-mismatch, got ${JSON.stringify(result)}`);
  }
});

test('missing subproof is reported as missing evidence with its path', () => {
  const record = createEvidenceRecord(policy, baseSnapshot, 'r8');
  const tampered = clone(record);
  const root = tampered.trace.subproofs[tampered.trace.root];
  const victim = root.children[0];
  delete tampered.trace.subproofs[victim];
  const result = verifyRecord(tampered, policy);
  assert.equal(result.status, 'rejected');
  if (result.status === 'rejected' && result.divergence.kind === 'missing-evidence') {
    assert.equal(result.divergence.path, '$/child[0]');
    assert.match(result.divergence.detail, /absent/);
  } else {
    assert.fail(`expected missing-evidence, got ${JSON.stringify(result)}`);
  }
});

test('sealed sensitive field: placeholder kept, conclusion unverifiable', () => {
  const strict: Policy = {
    id: 'strict',
    version: '1.0.0',
    rule: and(intCmp('age', 'ge', 18), intCmp('deposit', 'ge', 5000)),
  };
  const snapshot: AttributeSnapshot = { age: 34, deposit: sealAttribute('deposit', 9000, 's') };
  const record = createEvidenceRecord(strict, snapshot, 'r9');
  assert.equal(record.conclusion, 'unknown');
  const result = verifyRecord(record, strict);
  assert.equal(result.status, 'unverifiable');
  if (result.status === 'unverifiable') {
    assert.deepEqual(result.sealedAttributes, ['deposit']);
  }
});

test('source claims about sealed fields are not accepted', () => {
  const strict: Policy = {
    id: 'strict',
    version: '1.0.0',
    rule: and(intCmp('age', 'ge', 18), intCmp('deposit', 'ge', 5000)),
  };
  const snapshot: AttributeSnapshot = { age: 34, deposit: sealAttribute('deposit', 9000, 's') };
  const record = createEvidenceRecord(strict, snapshot, 'r10');
  const forged = clone(record);
  for (const read of forged.trace.reads) {
    if (read.attr === 'deposit') read.value = 999999; // source claims a value
  }
  const result = verifyRecord(forged, strict);
  assert.equal(result.status, 'rejected');
  if (result.status === 'rejected' && result.divergence.kind === 'sealed-claim-rejected') {
    assert.equal(result.divergence.attr, 'deposit');
  } else {
    assert.fail(`expected sealed-claim-rejected, got ${JSON.stringify(result)}`);
  }
});

test('shared subproofs are compressed: stored once, referenced twice', () => {
  const shared = intCmp('age', 'ge', 18);
  const score = intCmp('score', 'gt', 700);
  const rule = and(shared, or(score, shared));
  const { trace } = evaluate(rule, { age: 34, score: 650 });

  const sharedHash = hashRule(shared);
  const andHash = hashRule(rule);
  assert.equal(trace.root, andHash);
  assert.equal(Object.keys(trace.subproofs).length, 4); // and, or, shared leaf, score leaf
  assert.equal(trace.subproofs[andHash].children[0], sharedHash);
  const orHash = trace.subproofs[andHash].children[1];
  assert.equal(trace.subproofs[orHash].children[1], sharedHash);
  // the shared leaf was read once, not twice
  assert.deepEqual(trace.reads, [
    { attr: 'age', value: 34 },
    { attr: 'score', value: 650 },
  ]);
});

test('short-circuit skips are recorded and verified', () => {
  const p: Policy = {
    id: 'p-sc',
    version: '1.0.0',
    rule: and(intCmp('a', 'eq', 1), intCmp('b', 'eq', 2)),
  };
  const record = createEvidenceRecord(p, { a: 0, b: 2 }, 'r12');
  const root = record.trace.subproofs[record.trace.root];
  assert.equal(root.children.length, 1);
  assert.deepEqual(root.skipped, [hashRule(intCmp('b', 'eq', 2))]);
  assert.deepEqual(record.trace.reads, [{ attr: 'a', value: 0 }]);
  assert.deepEqual(verifyRecord(record, p), { status: 'verified', conclusion: false });
});

test('flipped conclusion claim is rejected', () => {
  const record = createEvidenceRecord(policy, baseSnapshot, 'r13');
  const tampered = clone(record);
  tampered.conclusion = 'false';
  const result = verifyRecord(tampered, policy);
  assert.equal(result.status, 'rejected');
  assert.equal(result.status === 'rejected' && result.divergence.kind, 'conclusion-mismatch');
});

test('tampered historical snapshot is detected via its hash', () => {
  const record = createEvidenceRecord(policy, baseSnapshot, 'r14');
  const tampered = clone(record);
  tampered.snapshot.score = 100;
  const result = verifyRecord(tampered, policy);
  assert.equal(result.status, 'rejected');
  assert.equal(result.status === 'rejected' && result.divergence.kind, 'snapshot-tampered');
});

test('not combinator and unknown propagation verify correctly', () => {
  const p: Policy = {
    id: 'p-not',
    version: '1.0.0',
    rule: not(intCmp('flag', 'eq', 1)),
  };
  const record = createEvidenceRecord(p, { flag: 0 }, 'r15');
  assert.deepEqual(verifyRecord(record, p), { status: 'verified', conclusion: true });

  const sealedRecord = createEvidenceRecord(p, { flag: sealAttribute('flag', 0, 's') }, 'r15b');
  assert.equal(verifyRecord(sealedRecord, p).status, 'unverifiable');
});

test('rule DSL rejects non-integer conditions', () => {
  assert.throws(() => intCmp('age', 'ge', 17.5), /integer/);
  assert.throws(() => inSet('region', []), /non-empty/);
});
