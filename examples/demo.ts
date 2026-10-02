import { HierarchicalArbiter, type ArbitrationResult } from "../src/index.ts";

function show(result: ArbitrationResult): void {
  console.log(`  决议: ${result.decision}`);
  console.log(`  拓扑: ${result.topology} | 纪元: ${result.epoch}`);
  console.log(`  权重: ${result.quorumWeight}/${result.requiredWeight} (达成/门限)`);
  for (const vote of result.votes) {
    console.log(`  选票: ${vote.regionId} 权重=${vote.weight} 签名=${vote.signature.slice(0, 24)}...`);
  }
  console.log(`  证据摘要: ${result.evidenceHash.slice(0, 32)}...`);
}

// 验收拓扑：IDC-A 权重 2（主中心），IDC-B / IDC-C 权重各 1
const arbiter = new HierarchicalArbiter([
  { id: "IDC-A", weight: 2, localNodes: 3, localMajorityThreshold: 2, isPrimary: true },
  { id: "IDC-B", weight: 1, localNodes: 3, localMajorityThreshold: 2 },
  { id: "IDC-C", weight: 1, localNodes: 3, localMajorityThreshold: 2 },
]);

console.log("=== 1. 正常分级拓扑：全区域参与仲裁 ===");
console.log(`门限 = ${arbiter.requiredWeight()}（总权重 4 的多数派）`);
const r1 = arbiter.arbitrate("config-rollout-v1");
show(r1);
console.log(`  证据验证: ${arbiter.verifyEvidence(r1) ? "通过" : "失败"}\n`);

console.log("=== 2. 主中心 IDC-A 失联：自动降级 ===");
arbiter.setReachable("IDC-A", false);
console.log(`拓扑 = ${arbiter.currentTopology}，纪元推进到 ${arbiter.currentEpoch}`);
console.log(`降级门限 = ${arbiter.requiredWeight()}（可达权重 2 的多数派）`);
const r2 = arbiter.arbitrate("emergency-failover");
show(r2);
console.log(`  B+C 临时多数派达成，证据验证: ${arbiter.verifyEvidence(r2) ? "通过" : "失败"}\n`);

console.log("=== 3. IDC-A 恢复：出示更高纪元重新接管 ===");
const takeover = arbiter.recoverPrimary(arbiter.currentEpoch + 1);
console.log(`接管者 = ${takeover.regionId}，出示纪元 = ${takeover.presentedEpoch}`);
console.log(`拓扑恢复 = ${arbiter.currentTopology}，当前纪元 = ${arbiter.currentEpoch}\n`);

console.log("=== 4. 纪元围栏：旧纪元决议被拒绝，无状态冲突 ===");
console.log(`  应用失联前决议(纪元${r1.epoch}): ${arbiter.applyDecision(r1) ? "接受" : "拒绝"}`);
console.log(`  应用降级期决议(纪元${r2.epoch}): ${arbiter.applyDecision(r2) ? "接受" : "拒绝"}`);
const r3 = arbiter.arbitrate("post-takeover-write");
console.log(`  应用新纪元决议(纪元${r3.epoch}): ${arbiter.applyDecision(r3) ? "接受" : "拒绝"}\n`);

console.log("=== 5. 证据防篡改 ===");
const tampered = { ...r3, decision: "forged-write" };
console.log(`  篡改决议内容后验证: ${arbiter.verifyEvidence(tampered) ? "通过" : "失败（符合预期）"}`);
