import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Ledger, GENESIS_HASH } from '../src/ledger.ts';
import { verifyProof } from '../src/merkle.ts';

function buildLedgerWith100Transfers(): Ledger {
  const ledger = new Ledger('test-authority');
  for (let i = 1; i <= 100; i++) {
    ledger.appendTransfer({
      from: i % 2 === 0 ? 'alice' : 'bob',
      to: i % 2 === 0 ? 'bob' : 'alice',
      amount: i * 10,
      asset: 'COIN',
      memo: `transfer #${i}`,
    });
  }
  return ledger;
}

test('appending 100 transfers builds a valid hash chain', () => {
  const ledger = buildLedgerWith100Transfers();
  assert.equal(ledger.height, 100);
  assert.equal(ledger.logicalTime, 100);
  assert.equal(ledger.verifyChain(), true);
  const first = ledger.getBlock(1);
  assert.equal(first.prevHash, GENESIS_HASH);
  for (let i = 2; i <= 100; i++) {
    assert.equal(ledger.getBlock(i).prevHash, ledger.getBlock(i - 1).hash);
  }
  const hashes = new Set(ledger.toJSON().map((b) => b.hash));
  assert.equal(hashes.size, 100);
});

test('every block carries a verifiable signature receipt', () => {
  const ledger = buildLedgerWith100Transfers();
  for (const block of ledger.toJSON()) {
    assert.equal(block.receipt.signer, 'test-authority');
    assert.equal(block.receipt.publicKey, ledger.publicKey);
    assert.match(block.receipt.signature, /^[0-9a-f]{128}$/);
  }
  assert.equal(ledger.verifyChain(), true);
});

test('reversing #50 appends compensating block #101 and updates balances', () => {
  const ledger = buildLedgerWith100Transfers();
  const aliceBefore = ledger.getBalance('alice');
  const bobBefore = ledger.getBalance('bob');
  const block50 = ledger.getBlock(50);
  const reversal = ledger.reverse(50);
  assert.equal(ledger.height, 101);
  assert.equal(reversal.index, 101);
  assert.equal(reversal.kind, 'REVERSAL');
  assert.equal(reversal.reversalOf, 50);
  assert.equal(reversal.prevHash, ledger.getBlock(100).hash);
  assert.deepEqual(
    {
      from: reversal.payload.from,
      to: reversal.payload.to,
      amount: reversal.payload.amount,
    },
    {
      from: block50.payload.to,
      to: block50.payload.from,
      amount: block50.payload.amount,
    },
  );
  assert.equal(ledger.getBalance('alice'), aliceBefore + 500);
  assert.equal(ledger.getBalance('bob'), bobBefore - 500);
  assert.equal(ledger.verifyChain(), true);
});

test('reversal never overwrites history: block #50 evidence stays intact', () => {
  const ledger = buildLedgerWith100Transfers();
  const before = ledger.getBlock(50);
  const rootAt100 = ledger.getRoot(100);
  const proofBefore = ledger.getProof(50);
  ledger.reverse(50);
  const after = ledger.getBlock(50);
  assert.deepEqual(after, before);
  assert.equal(ledger.isReversed(50), true);
  assert.equal(ledger.verifyProofAtHeight(proofBefore, 100), true);
  assert.equal(ledger.getRoot(100), rootAt100);
  const freshProof = ledger.getProof(50);
  assert.equal(ledger.verifyProofAtHeight(freshProof, 101), true);
  assert.equal(verifyProof(freshProof), true);
});

test('merkle proofs are available for every historical block', () => {
  const ledger = buildLedgerWith100Transfers();
  ledger.reverse(50);
  for (let i = 1; i <= ledger.height; i++) {
    const proof = ledger.getProof(i);
    assert.equal(ledger.verifyProofAtHeight(proof), true, `proof for #${i}`);
  }
});

test('tampered proofs and tampered history are rejected', () => {
  const ledger = buildLedgerWith100Transfers();
  const proof = ledger.getProof(50);
  const forgedLeaf = { ...proof, leafHash: 'a'.repeat(64) };
  assert.equal(ledger.verifyProofAtHeight(forgedLeaf), false);
  const forgedPath = {
    ...proof,
    path: proof.path.map((s, i) =>
      i === 0 ? { ...s, hash: 'b'.repeat(64) } : s,
    ),
  };
  assert.equal(ledger.verifyProofAtHeight(forgedPath), false);
  const wrongHeight = ledger.getProof(100);
  assert.equal(ledger.verifyProofAtHeight(wrongHeight, 99), false);
});

test('double reversal and invalid operations are rejected', () => {
  const ledger = buildLedgerWith100Transfers();
  ledger.reverse(50);
  assert.throws(() => ledger.reverse(50), /already been reversed/);
  assert.throws(() => ledger.reverse(101), /not a transfer/);
  assert.throws(() => ledger.reverse(999), /does not exist/);
  assert.throws(
    () =>
      ledger.appendTransfer({ from: 'a', to: 'b', amount: -5, asset: 'COIN' }),
    /positive/,
  );
});

test('ledger state is not mutable through returned blocks', () => {
  const ledger = buildLedgerWith100Transfers();
  const block = ledger.getBlock(50);
  assert.throws(() => {
    (block as { hash: string }).hash = 'tampered';
  }, TypeError);
  block.payload.amount = 999999;
  assert.equal(ledger.getBlock(50).payload.amount, 500);
});
