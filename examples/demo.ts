import { Ledger } from '../src/ledger.ts';

const ledger = new Ledger({
  initialBalances: { alice: 1_000_000, bob: 1_000_000, carol: 1_000_000, dave: 1_000_000 },
});

const accounts = ['alice', 'bob', 'carol', 'dave'];
for (let i = 0; i < 100; i++) {
  ledger.submitTransfer({
    from: accounts[i % 4],
    to: accounts[(i + 1) % 4],
    amount: (i + 1) * 10,
    memo: `transfer #${i + 1}`,
  });
}

console.log('=== 写入 100 笔转账 ===');
console.log('height:', ledger.height);
console.log('tip   :', ledger.tipHash);
console.log('chain :', ledger.verifyChain() ? 'VALID' : 'BROKEN');

const target = ledger.getBlock(49);
console.log('\n=== 第 50 笔（待冲正） ===');
console.log('block #50 hash:', target.hash);
console.log('payload       :', JSON.stringify(target.payload));

const proofBefore = ledger.getProof(49, 100);
console.log('merkle proof @height=100 valid:', Ledger.verifyProof(proofBefore, ledger.publicKeyPem));

const txId = (target.payload as { txId: string }).txId;
const reversal = ledger.reverse(txId, 'duplicate charge');

console.log('\n=== 冲正后 ===');
console.log('height:', ledger.height);
console.log('reversal block #101:', JSON.stringify(reversal.payload));
console.log('chain :', ledger.verifyChain() ? 'VALID' : 'BROKEN');
console.log('balances:', ledger.balancesSnapshot());

console.log('\n=== 历史证据 ===');
console.log('block #50 hash unchanged:', ledger.getBlock(49).hash === target.hash);
console.log('merkle root @height=100 unchanged:', ledger.merkleRoot(100) === proofBefore.merkle.root);
console.log('proof #50 @height=100 valid:', Ledger.verifyProof(ledger.getProof(49, 100), ledger.publicKeyPem));
console.log('proof #50 @height=101 valid:', Ledger.verifyProof(ledger.getProof(49, 101), ledger.publicKeyPem));
console.log('proof #101 @height=101 valid:', Ledger.verifyProof(ledger.getProof(100), ledger.publicKeyPem));
