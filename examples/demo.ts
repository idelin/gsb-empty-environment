import { recordEvidence } from '../src/evaluate.ts';
import type { Policy } from '../src/rule.ts';
import { fromAttributes, sealAttribute } from '../src/snapshot.ts';
import { verifyRecord } from '../src/verify.ts';

const policy: Policy = {
  name: 'loan-eligibility',
  version: 3,
  root: {
    kind: 'and',
    children: [
      {
        kind: 'and',
        children: [
          { kind: 'in', attr: 'tier', set: ['gold', 'platinum'] },
          { kind: 'int', attr: 'age', op: 'ge', value: 18 },
        ],
      },
      {
        kind: 'or',
        children: [
          { kind: 'int', attr: 'age', op: 'ge', value: 65 },
          { kind: 'int', attr: 'age', op: 'ge', value: 18 }, // shared subproof
        ],
      },
      { kind: 'not', child: { kind: 'in', attr: 'region', set: ['embargoed'] } },
    ],
  },
};

const liveAttributes = { age: 30, tier: 'gold', region: 'cn', income: 42000 };

console.log('== 1. record evidence (rule digest + snapshot + trace) ==');
const record = recordEvidence(policy, fromAttributes(liveAttributes));
console.log('policyDigest:', record.policyDigest);
console.log('readSet:', record.readSet.join(', '));
console.log('result:', record.result);
console.log('trace:', JSON.stringify(record.trace));

console.log('\n== 2. offline replay verification ==');
console.log(verifyRecord(record));

console.log('\n== 3. real-time attributes change afterwards; history is unaffected ==');
liveAttributes.age = 6;
liveAttributes.region = 'embargoed';
console.log(verifyRecord(record));

console.log('\n== 4. tamper: bump rule version ==');
const tamperedVersion = structuredClone(record);
tamperedVersion.policy.version = 4;
console.log(verifyRecord(tamperedVersion));

console.log('\n== 5. tamper: swap branch order ==');
const swapped = structuredClone(record);
if (swapped.trace.type === 'and') {
  const inner = swapped.trace.children[0];
  if (inner.type === 'and') inner.children = [inner.children[1], inner.children[0]];
}
console.log(verifyRecord(swapped));

console.log('\n== 6. tamper: delete one read item ==');
const deletedRead = structuredClone(record);
if (deletedRead.trace.type === 'and') {
  const inner = deletedRead.trace.children[0];
  if (inner.type === 'and') {
    const tierCond = inner.children[0];
    if (tierCond.type === 'cond') tierCond.reads = [];
  }
}
console.log(verifyRecord(deletedRead));

console.log('\n== 7. seal the sensitive tier field: conclusion becomes unverifiable ==');
const sealedRecord = recordEvidence(
  policy,
  sealAttribute(fromAttributes({ age: 30, tier: 'gold', region: 'cn', income: 42000 }), 'tier'),
);
console.log('sealed snapshot entry:', JSON.stringify(sealedRecord.snapshot.tier));
console.log(verifyRecord(sealedRecord));
