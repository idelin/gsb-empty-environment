import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Ledger, GENESIS_PREV_HASH } from '../src/ledger.ts';

const ACCOUNTS = ['alice', 'bob', 'carol', 'dave'];

function fundedLedger(): Ledger {
  return new Ledger({
    initialBalances: { alice: 1_000_000, bob: 1_000_000, carol: 1_000_000, dave: 1_000_000 },
  });
}

function fillHundredTransfers(ledger: Ledger): void {
  for (let i = 0; i < 100; i++) {
    const from = ACCOUNTS[i % ACCOUNTS.length];
    const to = ACCOUNTS[(i + 1) % ACCOUNTS.length];
    ledger.submitTransfer({ from, to, amount: (i + 1) * 10, memo: `transfer #${i + 1}` });
  }
}

test('写入100笔转账事务并生成完整哈希链', () => {
  const ledger = fundedLedger();
  fillHundredTransfers(ledger);

  assert.equal(ledger.height, 100);
  assert.equal(ledger.getBlock(0).prevHash, GENESIS_PREV_HASH);
  for (let i = 1; i < 100; i++) {
    assert.equal(ledger.getBlock(i).prevHash, ledger.getBlock(i - 1).hash);
    assert.equal(ledger.getBlock(i).index, i);
    assert.equal(ledger.getBlock(i).tick, i + 1);
  }
  assert.equal(ledger.tipHash, ledger.getBlock(99).hash);
  assert.ok(ledger.verifyChain());

  const balances = ledger.balancesSnapshot();
  assert.equal(
    balances.alice + balances.bob + balances.carol + balances.dave,
    4_000_000,
    '转账不改变资金总量',
  );
});

test('对第50笔发起冲正：新增第101笔记录，总账更新，历史证据保持有效', () => {
  const ledger = fundedLedger();
  fillHundredTransfers(ledger);

  const target = ledger.getBlock(49);
  assert.equal(target.payload.kind, 'transfer');
  const targetPayload = target.payload as Extract<typeof target.payload, { kind: 'transfer' }>;
  const fromBefore = ledger.balanceOf(targetPayload.from);
  const toBefore = ledger.balanceOf(targetPayload.to);

  const proofAt100 = ledger.getProof(49, 100);
  assert.ok(Ledger.verifyProof(proofAt100, ledger.publicKeyPem));
  const rootAt100 = ledger.merkleRoot(100);

  const targetHashBefore = target.hash;
  const targetReceiptBefore = target.receipt;

  const reversal = ledger.reverse(targetPayload.txId, 'duplicate charge');

  assert.equal(ledger.height, 101);
  assert.equal(reversal.index, 100);
  assert.equal(reversal.prevHash, ledger.getBlock(99).hash);
  assert.equal(reversal.payload.kind, 'reversal');
  const reversalPayload = reversal.payload as Extract<typeof reversal.payload, { kind: 'reversal' }>;
  assert.equal(reversalPayload.reversesTxId, targetPayload.txId);
  assert.equal(reversalPayload.from, targetPayload.to);
  assert.equal(reversalPayload.to, targetPayload.from);
  assert.equal(reversalPayload.amount, targetPayload.amount);
  assert.ok(ledger.isReversed(targetPayload.txId));

  assert.equal(ledger.balanceOf(targetPayload.from), fromBefore + targetPayload.amount);
  assert.equal(ledger.balanceOf(targetPayload.to), toBefore - targetPayload.amount);

  assert.equal(ledger.getBlock(49).hash, targetHashBefore, '原第50笔区块哈希不变');
  assert.equal(ledger.getBlock(49).receipt, targetReceiptBefore, '原第50笔签名回执不变');
  assert.ok(ledger.verifyChain());

  assert.equal(ledger.merkleRoot(100), rootAt100, '历史高度100的默克尔根不变');
  const historicalProof = ledger.getProof(49, 100);
  assert.deepEqual(historicalProof.merkle, proofAt100.merkle);
  assert.ok(Ledger.verifyProof(historicalProof, ledger.publicKeyPem), '历史版本路径证明仍有效');

  const currentProof = ledger.getProof(49, 101);
  assert.ok(Ledger.verifyProof(currentProof, ledger.publicKeyPem), '当前高度下证明仍有效');

  const reversalProof = ledger.getProof(100, 101);
  assert.ok(Ledger.verifyProof(reversalProof, ledger.publicKeyPem), '冲正记录自身也可被证明');
});

test('账簿只追加：区块深度冻结，物理覆写被拒绝', () => {
  const ledger = fundedLedger();
  ledger.submitTransfer({ from: 'alice', to: 'bob', amount: 100 });
  const block = ledger.getBlock(0);

  assert.ok(Object.isFrozen(block));
  assert.ok(Object.isFrozen(block.payload));
  assert.throws(() => {
    (block as { hash: string }).hash = 'tampered';
  }, TypeError);
  assert.throws(() => {
    (block.payload as { amount: number }).amount = 999;
  }, TypeError);
  assert.equal(ledger.height, 1);
  assert.ok(ledger.verifyChain());
});

test('防篡改：伪造证明与非法操作均被拒绝', () => {
  const ledger = fundedLedger();
  fillHundredTransfers(ledger);
  const proof = ledger.getProof(49);

  const forged = {
    block: { ...proof.block, hash: 'f'.repeat(64) },
    merkle: proof.merkle,
  };
  assert.equal(Ledger.verifyProof(forged, ledger.publicKeyPem), false);

  const wrongRoot = {
    block: proof.block,
    merkle: { ...proof.merkle, root: '0'.repeat(64) },
  };
  assert.equal(Ledger.verifyProof(wrongRoot, ledger.publicKeyPem), false);

  ledger.reverse('TX-0050');
  assert.throws(() => ledger.reverse('TX-0050'), /already been reversed/);
  assert.throws(() => ledger.reverse('TX-9999'), /unknown transaction/);
  assert.throws(() => ledger.reverse('TX-0101'), /not a transfer/);
  assert.throws(() =>
    ledger.submitTransfer({ from: 'alice', to: 'bob', amount: 10_000_000 }),
  /insufficient funds/);
  assert.throws(() => ledger.submitTransfer({ from: 'alice', to: 'bob', amount: -5 }));
  assert.throws(() => ledger.submitTransfer({ from: 'alice', to: 'alice', amount: 5 }));
});

test('离散时间：tick 严格递增且与写入顺序一致', () => {
  const ledger = fundedLedger();
  ledger.submitTransfer({ from: 'alice', to: 'bob', amount: 1 });
  ledger.submitTransfer({ from: 'bob', to: 'carol', amount: 1 });
  ledger.reverse('TX-0001');
  const ticks = [0, 1, 2].map((i) => ledger.getBlock(i).tick);
  assert.deepEqual(ticks, [1, 2, 3]);
});
