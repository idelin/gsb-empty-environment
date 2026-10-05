import { runAllScenarios } from "./scenarios.ts";

function fmt(ms: number): string {
  return `${ms.toFixed(1)}ms`;
}

console.log("探活模拟器报告");
console.log("指标说明：误判次数 与 检测延迟 为两个独立指标，不合并为单一分数。");
console.log("");

for (const scenario of runAllScenarios()) {
  const s = scenario.summary;
  console.log(`=== ${scenario.name} ===`);
  console.log(`场景: ${scenario.description}`);
  console.log(`误判次数 (false positives): ${s.falsePositives}`);
  if (s.detectionLatencyCount > 0) {
    const avg = fmt(s.detectionLatencyAvgMs);
    const p95 = fmt(s.detectionLatencyP95Ms);
    const max = fmt(s.detectionLatencyMaxMs);
    console.log(`检测延迟 (detection latency): avg=${avg} p95=${p95} max=${max} (n=${s.detectionLatencyCount})`);
  } else {
    console.log("检测延迟 (detection latency): 无真实故障样本");
  }
  console.log(
    `参考: 怀疑=${s.suspicions} 误怀疑=${s.falseSuspicions} ` +
      `正确移除=${s.trueRemovals} 旧心跳忽略=${s.staleHeartbeatsIgnored} ` +
      `移除后心跳=${s.postRemovalHeartbeats}`,
  );
  console.log("");
}
