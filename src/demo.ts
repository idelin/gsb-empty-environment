import { performance } from 'node:perf_hooks';
import { SpacetimeGridIndex } from './grid-index.ts';
import { proveIntersection } from './prover.ts';
import { verifyProof } from './certificate.ts';
import { CASE_A, CASE_B, MALL_ZONES } from './scenario.ts';

const THRESHOLD_M = 10;

function fmt(t: number): string {
  return new Date(t).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false });
}

// 1. 建库：时间切片倒排网格索引
const index = new SpacetimeGridIndex(/* cellSizeMeters */ 10, /* sliceMs */ 60_000);
index.addTrajectory(CASE_A);
index.addTrajectory(CASE_B);

// 2. 相交证明（计时；先预热一次以排除 JIT 编译开销）
proveIntersection(index, CASE_A.entityId, CASE_B.entityId, THRESHOLD_M, { zones: MALL_ZONES });
const t0 = performance.now();
const proof = proveIntersection(index, CASE_A.entityId, CASE_B.entityId, THRESHOLD_M, {
  zones: MALL_ZONES,
  generatedAt: Date.parse('2026-01-15T20:00:00+08:00'),
});
const elapsedMs = performance.now() - t0;

// 3. 输出报告
console.log('========== 时空相交追溯报告 ==========');
console.log(`实体A: ${proof.certificates[0]?.entityA ?? CASE_A.entityId}`);
console.log(`实体B: ${proof.certificates[0]?.entityB ?? CASE_B.entityId}`);
console.log(`距离阈值: ${proof.thresholdMeters} 米`);
console.log(`索引规模: ${index.sliceCount} 个时间切片`);
console.log(`求交耗时: ${elapsedMs.toFixed(3)} ms`);
console.log(`相交时段数: ${proof.certificates.length}`);
console.log('');
for (const cert of proof.certificates) {
  console.log(`--- 相交证书 #${cert.seq} ---`);
  console.log(`  时段:     ${fmt(cert.start)} ~ ${fmt(cert.end)}`);
  console.log(`  时长:     ${((cert.end - cert.start) / 1000).toFixed(1)} 秒`);
  console.log(`  地点:     ${cert.zone ?? '未知区域'} (坐标 ${cert.location.x.toFixed(1)}, ${cert.location.y.toFixed(1)}, ${cert.location.floor}F)`);
  console.log(`  最近距离: ${cert.minDistance.toFixed(2)} 米 @ ${fmt(cert.minDistanceAt)}`);
  console.log(`  证书哈希: ${cert.hash.slice(0, 16)}...  (前链 ${cert.prevHash.slice(0, 16)}...)`);
}
console.log('');
console.log(`轨迹承诺A: ${proof.trajectoryDigests[CASE_A.entityId].slice(0, 32)}...`);
console.log(`轨迹承诺B: ${proof.trajectoryDigests[CASE_B.entityId].slice(0, 32)}...`);
console.log(`证明根哈希: ${proof.rootHash}`);

// 4. 独立验证（不可抵赖性）
const verdict = verifyProof(proof, [CASE_A, CASE_B]);
console.log(`证明链验证: ${verdict.ok ? '通过 ✔' : '失败 ✘ ' + verdict.errors.join('; ')}`);

// 5. 篡改演示：改动任一字段即被识破
const tampered = structuredClone(proof);
tampered.certificates[0].zone = '服装柜台';
const forged = verifyProof(tampered, [CASE_A, CASE_B]);
console.log(`篡改检测演示(伪造地点): ${forged.ok ? '未检出 ✘' : '已检出 ✔ -> ' + forged.errors[0]}`);
