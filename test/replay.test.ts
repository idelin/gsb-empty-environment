import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { digest } from '../src/canonical.ts';
import type { EvidenceRecord, TraceNode } from '../src/evaluate.ts';
import { recordEvidence } from '../src/evaluate.ts';
import type { Policy, Rule } from '../src/rule.ts';
import { normalizePolicy } from '../src/rule.ts';
import type { Snapshot } from '../src/snapshot.ts';
import { fromAttributes, sealAttribute } from '../src/snapshot.ts';
import { verifyRecord } from '../src/verify.ts';

const cond = (attr: string, op: 'eq' | 'ne' | 'lt' | 'le' | 'gt' | 'ge', value: number): Rule => ({
  kind: 'int',
  attr,
  op,
  value,
});

const inSet = (attr: string, set: (string | number)[]): Rule => ({ kind: 'in', attr, set });

/**
 * Shared subproof: `age >= 18` appears twice, so the second occurrence
 * is compressed into a `ref` node pointing at the first evaluation.
 */
const policy: Policy = {
  name: 'loan-eligibility',
  version: 3,
  root: {
    kind: 'and',
    children: [
      { kind: 'and', children: [inSet('tier', ['gold', 'platinum']), cond('age', 'ge', 18)] },
      { kind: 'or', children: [cond('age', 'ge', 65), cond('age', 'ge', 18)] },
      { kind: 'not', child: inSet('region', ['embargoed']) },
    ],
  },
};

function freshAttributes(): Record<string, string | number | boolean> {
  return { age: 30, tier: 'gold', region: 'cn', income: 42000 };
}

function makeRecord(snapshot?: Snapshot): EvidenceRecord {
  return recordEvidence(policy, snapshot ?? fromAttributes(freshAttributes()));
}

function tamper(record: EvidenceRecord, mutate: (copy: EvidenceRecord) => void): EvidenceRecord {
  const copy = structuredClone(record);
  mutate(copy);
  return copy;
}

function childAt(node: TraceNode, ...indices: number[]): TraceNode {
  let current = node;
  for (const index of indices) {
    if (current.type !== 'and' && current.type !== 'or') throw new Error('not a combinator node');
    const next = current.children[index];
    if (!next) throw new Error(`no child at index ${index}`);
    current = next;
  }
  return current;
}

describe('offline replay verification', () => {
  it('verifies a genuine record and compresses shared subproofs', () => {
    const record = makeRecord();
    const report = verifyRecord(record);
    assert.equal(report.status, 'verified');
    assert.equal(report.result, true);
    assert.equal(record.policyDigest, digest(normalizePolicy(policy)));
    assert.deepEqual(record.readSet, ['age', 'region', 'tier']);

    const refNode = childAt(record.trace, 1, 1);
    assert.equal(refNode.type, 'ref', 'second age>=18 is compressed into a ref');
    if (refNode.type === 'ref') {
      const firstOccurrence = childAt(record.trace, 0, 1);
      assert.equal(refNode.target, firstOccurrence.id, 'ref points at the first evaluation');
    }
  });

  it('short-circuits and records the short-circuit flag', () => {
    const record = makeRecord(fromAttributes({ age: 12, tier: 'gold', region: 'cn' }));
    assert.equal(record.result, false);
    const root = record.trace;
    assert.equal(root.type, 'and');
    if (root.type === 'and') {
      assert.equal(root.shortCircuited, true);
      assert.equal(root.children.length, 1, 'remaining branches were not evaluated');
    }
    assert.equal(verifyRecord(record).status, 'verified');
  });

  it('is immune to later real-time attribute changes (historical snapshot wins)', () => {
    const attributes = freshAttributes();
    const record = makeRecord(fromAttributes(attributes));
    attributes.age = 6;
    attributes.tier = 'bronze';
    attributes.region = 'embargoed';
    const report = verifyRecord(record);
    assert.equal(report.status, 'verified');
    assert.equal(report.result, true);
  });

  it('rejects a tampered rule version via digest mismatch', () => {
    const tampered = tamper(makeRecord(), (copy) => {
      copy.policy.version = 4;
    });
    const report = verifyRecord(tampered);
    assert.equal(report.status, 'rejected');
    assert.equal(report.divergence?.path, '$.policyDigest');
    assert.equal(report.divergence?.reason, 'policy-digest-mismatch');
  });

  it('rejects the same trace paired with a different rule digest', () => {
    const other: Policy = { name: 'loan-eligibility', version: 99, root: cond('age', 'ge', 21) };
    const tampered = tamper(makeRecord(), (copy) => {
      copy.policyDigest = digest(normalizePolicy(other));
    });
    const report = verifyRecord(tampered);
    assert.equal(report.status, 'rejected');
    assert.equal(report.divergence?.reason, 'policy-digest-mismatch');
  });

  it('locates the first divergence when a read item is deleted', () => {
    const tampered = tamper(makeRecord(), (copy) => {
      const tierCond = childAt(copy.trace, 0, 0);
      if (tierCond.type !== 'cond') throw new Error('unexpected trace shape');
      tierCond.reads = [];
    });
    const report = verifyRecord(tampered);
    assert.equal(report.status, 'rejected');
    assert.equal(report.divergence?.path, '$.children[0].children[0].reads');
    assert.equal(report.divergence?.reason, 'read-mismatch');
  });

  it('cannot replay when actual read fields are omitted', () => {
    const tampered = tamper(makeRecord(), (copy) => {
      const tierCond = childAt(copy.trace, 0, 0);
      if (tierCond.type !== 'cond') throw new Error('unexpected trace shape');
      tierCond.reads = [{ attr: 'tier', kind: 'value' } as never];
    });
    const report = verifyRecord(tampered);
    assert.equal(report.status, 'rejected');
    assert.equal(report.divergence?.path, '$.children[0].children[0].reads');
    assert.equal(report.divergence?.reason, 'read-mismatch');
  });

  it('locates the first divergence when short-circuit order is swapped', () => {
    const tampered = tamper(makeRecord(), (copy) => {
      const innerAnd = childAt(copy.trace, 0);
      if (innerAnd.type !== 'and') throw new Error('unexpected trace shape');
      innerAnd.children = [innerAnd.children[1], innerAnd.children[0]];
    });
    const report = verifyRecord(tampered);
    assert.equal(report.status, 'rejected');
    assert.equal(report.divergence?.path, '$.children[0].children[0].id');
    assert.equal(report.divergence?.reason, 'node-id-mismatch');
  });

  it('rejects a forged shared-subproof reference', () => {
    const tampered = tamper(makeRecord(), (copy) => {
      const orNode = childAt(copy.trace, 1);
      if (orNode.type !== 'or') throw new Error('unexpected trace shape');
      orNode.children[1] = { id: 6, type: 'ref', target: 0 };
    });
    const report = verifyRecord(tampered);
    assert.equal(report.status, 'rejected');
    assert.equal(report.divergence?.reason, 'shared-subproof-mismatch');
  });

  it('marks sealed-field conclusions unverifiable instead of trusting claims', () => {
    const sealed = sealAttribute(fromAttributes({ age: 30, tier: 'gold', region: 'cn' }), 'tier');
    const record = recordEvidence(policy, sealed);
    assert.equal(record.result, 'unverifiable');
    const report = verifyRecord(record);
    assert.equal(report.status, 'unverifiable');
    assert.equal(report.result, 'unverifiable');

    const forged = tamper(record, (copy) => {
      const tierCond = childAt(copy.trace, 0, 0);
      if (tierCond.type !== 'cond') throw new Error('unexpected trace shape');
      tierCond.result = true;
      copy.result = true;
    });
    const forgedReport = verifyRecord(forged);
    assert.equal(forgedReport.status, 'rejected');
    assert.equal(forgedReport.divergence?.reason, 'sealed-claim-not-accepted');
    assert.equal(forgedReport.divergence?.path, '$.children[0].children[0].result');
  });

  it('seals sensitive fields independently while others stay verifiable', () => {
    const sealed = sealAttribute(fromAttributes(freshAttributes()), 'income');
    assert.equal(sealed.income?.kind, 'sealed');
    assert.equal(sealed.tier?.kind, 'value');
    const record = recordEvidence(policy, sealed);
    assert.equal(verifyRecord(record).status, 'verified', 'income is never read by the policy');
  });

  it('identifies missing evidence in the snapshot', () => {
    const tampered = tamper(makeRecord(), (copy) => {
      delete copy.snapshot.tier;
    });
    const report = verifyRecord(tampered);
    assert.equal(report.status, 'rejected');
    assert.equal(report.divergence?.path, '$.children[0].children[0].reads');
    assert.equal(report.divergence?.reason, 'missing-evidence');
  });

  it('rejects a tampered final boolean and a tampered read set', () => {
    const record = makeRecord();
    const flipped = tamper(record, (copy) => {
      copy.result = false;
    });
    const flipReport = verifyRecord(flipped);
    assert.equal(flipReport.status, 'rejected');
    assert.equal(flipReport.divergence?.path, '$.result');

    const pruned = tamper(record, (copy) => {
      copy.readSet = copy.readSet.filter((attr) => attr !== 'tier');
    });
    const pruneReport = verifyRecord(pruned);
    assert.equal(pruneReport.status, 'rejected');
    assert.equal(pruneReport.divergence?.path, '$.readSet');
    assert.equal(pruneReport.divergence?.reason, 'readset-mismatch');
  });

  it('rejects a trace whose branch result was recomputed dishonestly', () => {
    const tampered = tamper(makeRecord(), (copy) => {
      const notNode = childAt(copy.trace, 2);
      if (notNode.type !== 'not') throw new Error('unexpected trace shape');
      notNode.result = false;
    });
    const report = verifyRecord(tampered);
    assert.equal(report.status, 'rejected');
    assert.equal(report.divergence?.path, '$.children[2].result');
    assert.equal(report.divergence?.reason, 'result-mismatch');
  });
});
