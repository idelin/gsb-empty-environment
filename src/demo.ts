import { ErasableComplianceTree, generateOfficerKeyPair } from './erasableTree.ts';

const short = (hex: string | null) => (hex ? hex.slice(0, 16) + '…' : 'null');

console.log('=== 可擦除合规数据脱敏证明器 · 运行样例 ===\n');

// 1. 合规官密钥与证明器初始化
const officer = generateOfficerKeyPair();
const tree = new ErasableComplianceTree({
  treeId: 'compliance-tree-2026',
  officerPublicKey: officer.publicKey,
  officerPrivateKey: officer.privateKey,
});

// 2. 注册三位客户
const alice = { customerId: 'C-001', name: 'Alice', idCard: '1101****0001', tier: 'gold' };
const bob = { customerId: 'C-002', name: 'Bob', idCard: '3101****0002', tier: 'silver' };
const carol = { customerId: 'C-003', name: 'Carol', idCard: '4401****0003', tier: 'platinum' };

const iAlice = tree.register(alice);
const iBob = tree.register(bob);
const iCarol = tree.register(carol);
console.log(`注册 3 位客户，初始树根: ${short(tree.rootHex())}`);

// 3. 擦除前，Bob 与 Carol 各自取得存证路径
const bobReceipt = tree.proveInclusion(iBob);
const carolReceipt = tree.proveInclusion(iCarol);
console.log(`擦除前 Bob 存证可验: ${ErasableComplianceTree.verifyInclusion(bob, bobReceipt, tree.rootHex()!)}`);
console.log(`擦除前 Carol 存证可验: ${ErasableComplianceTree.verifyInclusion(carol, carolReceipt, tree.rootHex()!)}`);

// 4. Alice 行使被遗忘权：注销并删除个人记录
console.log(`\n--- 客户 ${alice.customerId} 请求注销并删除个人记录（GDPR 被遗忘权） ---`);
const eraseReceipt = tree.erase(iAlice);
console.log(`叶子 #${iAlice} 已重写为 EraseMarker: ${short(eraseReceipt.eraseMarkerHash)}`);
console.log(`删除签名(Ed25519): ${short(eraseReceipt.deletionSignature)}`);
console.log(`树根平滑演进: ${short(eraseReceipt.rootBefore)} -> ${short(eraseReceipt.rootAfter)}`);

// 5. 物理擦除确认：原明文已不可恢复
try {
  tree.readRecord(iAlice);
  console.log('!! 明文仍可读取，擦除失败');
} catch (e) {
  console.log(`原客户数据已物理擦除: ${(e as Error).message}`);
}

// 6. 审计方独立核验擦除证明（无需接触任何明文）
const auditOk = ErasableComplianceTree.verifyErase(eraseReceipt, tree.rootHex()!);
console.log(`审计方核验擦除证明（签名+标记在根上）: ${auditOk}`);

// 7. 关键验收：其余客户凭公开擦除回执将旧存证本地演进，对新根依然完全有效
const bobEvolved = ErasableComplianceTree.updateInclusionReceipt(bobReceipt, eraseReceipt);
const carolEvolved = ErasableComplianceTree.updateInclusionReceipt(carolReceipt, eraseReceipt);
console.log(`\n擦除后 Bob 旧存证演进后对新根可验: ${ErasableComplianceTree.verifyInclusion(bob, bobEvolved, tree.rootHex()!)}`);
console.log(`擦除后 Carol 旧存证演进后对新根可验: ${ErasableComplianceTree.verifyInclusion(carol, carolEvolved, tree.rootHex()!)}`);
// 树亦可持续为未注销客户出具有效存证
console.log(`树重新出具的 Bob 存证可验: ${ErasableComplianceTree.verifyInclusion(bob, tree.proveInclusion(iBob), tree.rootHex()!)}`);

// 8. 已擦除记录不再能通过存证验证
console.log(`已擦除的 Alice 存证验证（应为 false）: ${ErasableComplianceTree.verifyInclusion(alice, tree.proveInclusion(iAlice), tree.rootHex()!)}`);
