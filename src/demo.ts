import { IntersectionProver, verifyProof } from "./prover.ts";
import { buildMallScenario } from "./scenario.ts";

const TIME_ZONE = "Asia/Shanghai";

function fmt(t: number): string {
  return (
    new Date(t).toLocaleString("zh-CN", {
      timeZone: TIME_ZONE,
      hour12: false,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      fractionalSecondDigits: 3,
    }) + " (UTC+08:00)"
  );
}

function main(): void {
  const { index, entityA, entityB, thresholdMeters } = buildMallScenario();
  console.log("=== 四维时空反向索引 · 历史相交证明器 ===");
  console.log("场景：疾控流调 / 星光购物中心 / 2026-09-30 上午");
  console.log(`索引：${index.entityIds().length} 个实体，${index.sliceCount()} 个时间切片`);
  console.log(`查询：${entityA} 与 ${entityB} 是否曾相距 < ${thresholdMeters} 米\n`);

  const prover = new IntersectionProver(index);
  const proof = prover.prove(entityA, entityB, thresholdMeters);

  console.log(`时空求交耗时：${proof.queryDurationMs.toFixed(3)} ms`);
  console.log(`发现 ${proof.intervals.length} 段相遇接触时段：\n`);

  proof.intervals.forEach((iv, i) => {
    console.log(`【相遇 #${i + 1}】`);
    console.log(`  开始：${fmt(iv.tStart)}`);
    console.log(`  结束：${fmt(iv.tEnd)}`);
    console.log(`  持续：${(iv.durationMs / 60000).toFixed(2)} 分钟`);
    console.log(`  最近距离：${iv.minDistanceMeters.toFixed(3)} 米`);
    console.log(
      `  地点：${iv.location.label}  (${iv.location.x.toFixed(1)}, ${iv.location.y.toFixed(1)}, ${iv.location.z.toFixed(1)})`,
    );
    console.log(`  证书哈希：${proof.certificates[i].hash}`);
    console.log();
  });

  console.log("--- 不可抵赖承诺 ---");
  for (const c of proof.commitments) {
    console.log(
      `  ${c.entityId}：${c.pointCount} 个轨迹点，Merkle 根 ${c.merkleRoot}`,
    );
  }
  console.log(`  证明链摘要（proofDigest）：${proof.proofDigest}\n`);

  const verification = verifyProof(proof);
  console.log("--- 独立验证 ---");
  for (const r of verification.certificateResults) {
    console.log(`  证书 ${r.certificateId.slice(0, 16)}… 校验：${r.ok ? "通过" : "失败"}`);
  }
  console.log(`  整体验证结论：${verification.ok ? "通过，证明材料不可抵赖" : "失败"}`);
  if (!verification.ok) {
    console.error(verification.errors.join("\n"));
    process.exitCode = 1;
  }
}

main();
