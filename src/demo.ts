import { Ledger } from './ledger.ts';

const ledger = new Ledger('audit-authority-01');

console.log('=== 1. Append 100 transfer transactions ===');
for (let i = 1; i <= 100; i++) {
  ledger.appendTransfer({
    from: i % 2 === 0 ? 'alice' : 'bob',
    to: i % 2 === 0 ? 'bob' : 'alice',
    amount: i * 10,
    asset: 'COIN',
    memo: `transfer #${i}`,
  });
}
console.log(`height            : ${ledger.height}`);
console.log(`logical clock     : ${ledger.logicalTime}`);
console.log(`chain valid       : ${ledger.verifyChain()}`);
console.log(`merkle root @100  : ${ledger.getRoot(100)}`);
console.log(`alice balance     : ${ledger.getBalance('alice')}`);
console.log(`bob balance       : ${ledger.getBalance('bob')}`);

console.log('\n=== 2. Merkle proof for block #50 (before reversal) ===');
const proof50 = ledger.getProof(50);
console.log(`proof path length : ${proof50.path.length}`);
console.log(`proof verifies    : ${ledger.verifyProofAtHeight(proof50, 100)}`);
const block50Before = ledger.getBlock(50);
console.log(`block #50 hash    : ${block50Before.hash}`);

console.log('\n=== 3. Reverse transaction #50 (compensating entry) ===');
const reversal = ledger.reverse(50, 'disputed transfer, chargeback');
console.log(`reversal block    : #${reversal.index} (kind=${reversal.kind}, reversalOf=${reversal.reversalOf})`);
console.log(`reversal payload  : ${reversal.payload.from} -> ${reversal.payload.to} ${reversal.payload.amount} ${reversal.payload.asset}`);
console.log(`height            : ${ledger.height}`);
console.log(`chain valid       : ${ledger.verifyChain()}`);
console.log(`alice balance     : ${ledger.getBalance('alice')}`);
console.log(`bob balance       : ${ledger.getBalance('bob')}`);

console.log('\n=== 4. History is immutable: block #50 evidence still valid ===');
const block50After = ledger.getBlock(50);
console.log(`block #50 unchanged      : ${block50After.hash === block50Before.hash}`);
console.log(`block #50 marked reversed: ${ledger.isReversed(50)}`);
console.log(`old proof vs root@100    : ${ledger.verifyProofAtHeight(proof50, 100)}`);
const freshProof50 = ledger.getProof(50);
console.log(`new proof vs root@101    : ${ledger.verifyProofAtHeight(freshProof50, 101)}`);
const proof101 = ledger.getProof(101);
console.log(`reversal proof vs @101   : ${ledger.verifyProofAtHeight(proof101, 101)}`);

console.log('\n=== 5. Tamper check: forged proof must fail ===');
const forged = { ...freshProof50, leafHash: 'f'.repeat(64) };
console.log(`forged proof verifies    : ${ledger.verifyProofAtHeight(forged, 101)}`);
