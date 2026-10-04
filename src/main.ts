import { scenarios } from "./scenarios.ts";

console.log("探活模拟器 · 离散事件仿真报告");
console.log("误判（误疑/误删）与检测延迟为相互独立的指标，分别报告，不合成单一分数。\n");

for (const sc of scenarios) {
  const { sim, durationMs } = sc.create();
  sim.run(durationMs);
  sim.finish();

  const m = sim.metrics;
  const lat = m.latencyStats();

  console.log(`■ ${sc.name}（时长 ${durationMs / 1000}s）`);
  console.log(`  场景: ${sc.description}`);
  console.log(`  误判:   误疑 ${m.falseSuspects} 次 | 误删 ${m.falseRemovals} 次`);
  console.log(
    lat
      ? `  检测延迟: ${lat.count} 次真实故障 | 平均 ${lat.avgMs.toFixed(1)}ms | 最大 ${lat.maxMs}ms | 漏检 ${m.missedDetections} 次`
      : `  检测延迟: 无真实故障 | 漏检 ${m.missedDetections} 次`,
  );
  console.log(
    `  其他:   怀疑后恢复 ${m.recoveries} 次 | 移除后重加入 ${m.rejoins} 次 | 旧心跳忽略 ${sim.detector.staleIgnored} 次`,
  );
  console.log();
}
