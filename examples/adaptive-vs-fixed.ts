import { Simulation } from "../src/sim.ts";
import { defaultMonitorConfig, longTailDelaySampler } from "../src/scenarios.ts";
import type { MonitorConfig } from "../src/monitor.ts";

function runLongTail(monitor: Partial<MonitorConfig>): { falseSuspects: number; falseRemovals: number } {
  const sim = new Simulation({
    monitor: defaultMonitorConfig(monitor),
    tickMs: 25,
    delaySampler: longTailDelaySampler(3),
  });
  sim.addNode("n1");
  sim.run(60_000);
  sim.finish();
  return { falseSuspects: sim.metrics.falseSuspects, falseRemovals: sim.metrics.falseRemovals };
}

const adaptive = runLongTail({});
const fixed150 = runLongTail({ minSuspectTimeoutMs: 150, maxSuspectTimeoutMs: 150 });
const fixed2000 = runLongTail({ minSuspectTimeoutMs: 2000, maxSuspectTimeoutMs: 2000 });

console.log("长尾延迟场景：自适应窗口 vs 固定窗口（误判指标，独立报告）:");
console.log(`  自适应窗口  : 误疑 ${adaptive.falseSuspects} 次 | 误删 ${adaptive.falseRemovals} 次`);
console.log(`  固定 150ms  : 误疑 ${fixed150.falseSuspects} 次 | 误删 ${fixed150.falseRemovals} 次`);
console.log(`  固定 2000ms : 误疑 ${fixed2000.falseSuspects} 次 | 误删 ${fixed2000.falseRemovals} 次`);
