import { createEvidenceRecord } from './evidence.ts';
import type { EvidenceRecord } from './evidence.ts';
import { verifyRecord } from './replay.ts';
import { and, inSet, intCmp, or } from './rule.ts';
import type { Policy } from './rule.ts';
import { sealAttribute } from './snapshot.ts';
import type { AttributeSnapshot } from './snapshot.ts';
import { loadRecord, saveRecord } from './store.ts';

const RECORDS_DIR = new URL('../records', import.meta.url).pathname;

function show(label: string, value: unknown): void {
  console.log(`\n=== ${label} ===`);
  console.log(typeof value === 'string' ? value : JSON.stringify(value, null, 2));
}

// --- Policy: integer conditions + finite set + boolean combinators only ----
const policy: Policy = {
  id: 'loan-eligibility',
  version: '1.0.0',
  rule: and(
    inSet('region', [1, 2, 3]),
    intCmp('age', 'ge', 18),
    or(intCmp('score', 'gt', 700), intCmp('deposit', 'ge', 5000)),
  ),
};

// --- Live attribute source (stand-in for a real-time provider) -------------
const liveAttributes: Record<string, number> = { region: 2, age: 34, score: 712, deposit: 1200 };

// 1. Snapshot the explicit attributes, evaluate, persist the evidence record.
const snapshot: AttributeSnapshot = { ...liveAttributes };
const record = createEvidenceRecord(policy, snapshot, 'demo-loan-001');
const file = saveRecord(RECORDS_DIR, record);
show('1. evidence record saved', {
  file,
  ruleSummary: record.ruleSummary,
  snapshotHash: record.snapshotHash,
  conclusion: record.conclusion,
  subproofs: Object.keys(record.trace.subproofs).length,
  reads: record.trace.reads,
});

// 2. Real-time attributes are "corrected" AFTER the decision.
liveAttributes.score = 100;
liveAttributes.region = 9;
show('2. live attributes corrected after the fact', liveAttributes);

// 3. Offline replay: historical verdict is unaffected by the correction.
const reloaded = loadRecord(RECORDS_DIR, 'demo-loan-001');
show('3. offline replay of historical record', verifyRecord(reloaded, policy));

// 4. Tamper: bump the rule version.
show('4. tampered rule version', verifyRecord(reloaded, { ...policy, version: '9.9.9' }));

// 5. Tamper: delete one actual read item.
const deletedRead: EvidenceRecord = structuredClone(reloaded);
deletedRead.trace.reads.splice(1, 1);
show('5. one read item deleted', verifyRecord(deletedRead, policy));

// 6. Tamper: swap short-circuit evaluation order inside the or-branch.
const swapped: EvidenceRecord = structuredClone(reloaded);
const swappedNode = Object.values(swapped.trace.subproofs).find((n) => n.children.length >= 2);
if (swappedNode) swappedNode.children.reverse();
show('6. short-circuit order swapped', verifyRecord(swapped, policy));

// 7. Sealed sensitive field: conclusion blocked -> unverifiable.
const sealedSnapshot: AttributeSnapshot = {
  region: 2,
  age: 34,
  score: 712,
  deposit: sealAttribute('deposit', 1200, 'demo-salt'),
};
const sealedPolicy: Policy = {
  id: 'loan-eligibility-strict',
  version: '1.0.0',
  rule: and(inSet('region', [1, 2, 3]), intCmp('age', 'ge', 18), intCmp('deposit', 'ge', 5000)),
};
const sealedRecord = createEvidenceRecord(sealedPolicy, sealedSnapshot, 'demo-loan-sealed');
show('7. sealed sensitive field', verifyRecord(sealedRecord, sealedPolicy));

// 8. Forgery: source claims a concrete value for the sealed field.
const forged: EvidenceRecord = structuredClone(sealedRecord);
forged.trace.reads.push({ attr: 'deposit', value: 999999 });
show('8. forged claim about sealed field', verifyRecord(forged, sealedPolicy));
