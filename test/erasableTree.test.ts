import { test } from 'node:test';
import assert from 'node:assert/strict';
import { generateKeyPairSync, sign } from 'node:crypto';
import {
  ErasableComplianceTree,
  generateOfficerKeyPair,
  deletionMessage,
  type CustomerRecord,
} from '../src/erasableTree.ts';
import { hashEraseMarker } from '../src/crypto.ts';

function setup(count = 5) {
  const officer = generateOfficerKeyPair();
  const tree = new ErasableComplianceTree({
    treeId: 'test-tree',
    officerPublicKey: officer.publicKey,
    officerPrivateKey: officer.privateKey,
  });
  const records: CustomerRecord[] = [];
  for (let i = 0; i < count; i++) {
    records.push({ customerId: `C-${String(i).padStart(3, '0')}`, name: `User${i}`, idCard: `ID-${i}` });
    tree.register(records[i]);
  }
  return { officer, tree, records };
}

test('注册记录后可为每位客户生成并验证存证路径', () => {
  const { tree, records } = setup(5);
  const root = tree.rootHex()!;
  for (let i = 0; i < records.length; i++) {
    const receipt = tree.proveInclusion(i);
    assert.equal(ErasableComplianceTree.verifyInclusion(records[i], receipt, root), true);
  }
});

test('擦除：叶子重写为 EraseMarker，树根平滑演进', () => {
  const { tree } = setup(4);
  const rootBefore = tree.rootHex()!;
  const receipt = tree.erase(1);
  const rootAfter = tree.rootHex()!;

  assert.notEqual(rootBefore, rootAfter, '擦除后根必须演进');
  assert.equal(receipt.rootBefore, rootBefore);
  assert.equal(receipt.rootAfter, rootAfter);
  assert.equal(tree.isErased(1), true);
  assert.notEqual(receipt.eraseMarkerHash, receipt.originalLeafHash, '标记哈希必须不同于原叶哈希');
});

test('擦除后原客户明文被物理擦除，不可恢复', () => {
  const { tree } = setup(3);
  tree.erase(0);
  assert.throws(() => tree.readRecord(0), /物理擦除/);
  // 其余记录不受影响
  assert.equal(tree.readRecord(1).name, 'User1');
});

test('验收核心：擦除一条记录后，其余客户存证路径对新根依然完全有效', () => {
  const { tree, records } = setup(7);
  // 擦除前为所有客户取证
  const receipts = records.map((_, i) => tree.proveInclusion(i));
  const eraseReceipt = tree.erase(3); // 客户 C-003 注销
  const newRoot = tree.rootHex()!;
  for (let i = 0; i < records.length; i++) {
    if (i === 3) continue;
    // 方式一：旧存证凭公开擦除回执本地演进，无需访问树
    const evolved = ErasableComplianceTree.updateInclusionReceipt(receipts[i], eraseReceipt);
    assert.equal(
      ErasableComplianceTree.verifyInclusion(records[i], evolved, newRoot),
      true,
      `客户 #${i} 演进后的旧存证应对新根有效`,
    );
    // 方式二：树重新出具的存证
    assert.equal(ErasableComplianceTree.verifyInclusion(records[i], tree.proveInclusion(i), newRoot), true);
  }
  // 被擦除客户的存证不再有效，且不可演进
  assert.equal(ErasableComplianceTree.verifyInclusion(records[3], receipts[3], newRoot), false);
  assert.throws(() => ErasableComplianceTree.updateInclusionReceipt(receipts[3], eraseReceipt), /不可演进/);
});

test('擦除证明可被审计方独立核验（签名 + 标记在根上）', () => {
  const { tree } = setup(4);
  const receipt = tree.erase(2);
  assert.equal(ErasableComplianceTree.verifyErase(receipt, tree.rootHex()!), true);
  // 不传入根时使用回执自带 rootAfter
  assert.equal(ErasableComplianceTree.verifyErase(receipt), true);
});

test('篡改检测：伪造签名 / 错根 / 错叶子均无法通过擦除验证', () => {
  const { tree } = setup(4);
  const receipt = tree.erase(1);

  // 伪造签名（另一位“合规官”签的）
  const attacker = generateKeyPairSync('ed25519');
  const fakeMsg = deletionMessage(receipt.treeId, receipt.leafIndex, Buffer.from(receipt.originalLeafHash, 'hex'), receipt.erasedAt);
  const fakeSig = sign(null, fakeMsg, attacker.privateKey).toString('hex');
  assert.equal(ErasableComplianceTree.verifyErase({ ...receipt, deletionSignature: fakeSig }), false);

  // 错误的期望根
  const { tree: other } = setup(4);
  assert.equal(ErasableComplianceTree.verifyErase(receipt, other.rootHex()!), false);

  // 篡改叶子下标
  assert.equal(ErasableComplianceTree.verifyErase({ ...receipt, leafIndex: 2 }), false);
});

test('擦除标记结构：H(erase | idx | 原叶哈希 | 删除签名)', () => {
  const { tree } = setup(2);
  const receipt = tree.erase(0);
  const expected = hashEraseMarker(
    receipt.leafIndex,
    Buffer.from(receipt.originalLeafHash, 'hex'),
    Buffer.from(receipt.deletionSignature, 'hex'),
  ).toString('hex');
  assert.equal(receipt.eraseMarkerHash, expected);
});

test('重复擦除与越界访问被拒绝', () => {
  const { tree } = setup(2);
  tree.erase(0);
  assert.throws(() => tree.erase(0), /已处于擦除状态/);
  assert.throws(() => tree.erase(99), /越界/);
});

test('连续多次擦除：根持续演进，剩余存证始终有效', () => {
  const { tree, records } = setup(6);
  const receipts = records.map((_, i) => tree.proveInclusion(i));
  const roots: string[] = [tree.rootHex()!];
  const eraseReceipts = [];
  for (const idx of [0, 2, 4]) {
    eraseReceipts.push(tree.erase(idx));
    roots.push(tree.rootHex()!);
  }
  assert.equal(new Set(roots).size, roots.length, '每次擦除都应产生新根');
  const finalRoot = tree.rootHex()!;
  for (const i of [1, 3, 5]) {
    // 旧存证依次随三次擦除连续演进，最终仍对新根有效
    let receipt = receipts[i];
    for (const er of eraseReceipts) receipt = ErasableComplianceTree.updateInclusionReceipt(receipt, er);
    assert.equal(ErasableComplianceTree.verifyInclusion(records[i], receipt, finalRoot), true);
  }
});

test('奇数叶子与单片叶子树的边界情形', () => {
  const single = setup(1);
  const r0 = single.tree.proveInclusion(0);
  assert.equal(ErasableComplianceTree.verifyInclusion(single.records[0], r0, single.tree.rootHex()!), true);
  const eraseReceipt = single.tree.erase(0);
  assert.equal(ErasableComplianceTree.verifyErase(eraseReceipt, single.tree.rootHex()!), true);

  const odd = setup(9);
  const receipts = odd.records.map((_, i) => odd.tree.proveInclusion(i));
  const er = odd.tree.erase(8); // 擦除最后一片（晋升路径上的）叶子
  const root = odd.tree.rootHex()!;
  for (let i = 0; i < 8; i++) {
    const evolved = ErasableComplianceTree.updateInclusionReceipt(receipts[i], er);
    assert.equal(ErasableComplianceTree.verifyInclusion(odd.records[i], evolved, root), true);
  }
});

test('只持有公钥的验证实例不能签发删除', () => {
  const { officer } = setup(1);
  const verifier = new ErasableComplianceTree({ treeId: 'v', officerPublicKey: officer.publicKey });
  verifier.register({ customerId: 'X' });
  assert.throws(() => verifier.erase(0), /未持有合规官私钥/);
});
