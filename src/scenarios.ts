import type { MetricsSummary } from "./metrics.ts";
import { congestedDelay, constantDelay, mulberry32, uniformDelay } from "./random.ts";
import { Simulation } from "./simulation.ts";

export interface ScenarioResult {
  name: string;
  description: string;
  summary: MetricsSummary;
}

export function healthyBaseline(): ScenarioResult {
  const rng = mulberry32(101);
  const sim = new Simulation({ delaySampler: uniformDelay(rng, 5, 50) });
  sim.addNode("n1");
  sim.addNode("n2");
  sim.addNode("n3");
  const summary = sim.run(60_000);
  return {
    name: "healthy-baseline",
    description: "3 个健康节点，网络抖动 5~50ms，运行 60s",
    summary,
  };
}

export function briefPause(): ScenarioResult {
  const sim = new Simulation({ delaySampler: constantDelay(10) });
  sim.addNode("n1");
  sim.schedulePause("n1", 10_000, 3_500);
  const summary = sim.run(30_000);
  return {
    name: "brief-pause",
    description: "节点暂停 3.5s（约 3.5 个心跳间隔）：应先怀疑、随后凭当前代次心跳恢复",
    summary,
  };
}

export function longPause(): ScenarioResult {
  const sim = new Simulation({ delaySampler: constantDelay(10) });
  sim.addNode("n1");
  sim.schedulePause("n1", 10_000, 8_000);
  const summary = sim.run(30_000);
  return {
    name: "long-pause",
    description: "节点暂停 8s（超过怀疑窗口）：被误移除，计 1 次误判",
    summary,
  };
}

export function cleanCrash(): ScenarioResult {
  const sim = new Simulation({ delaySampler: constantDelay(10) });
  sim.addNode("n1");
  sim.scheduleCrash("n1", 20_000);
  const summary = sim.run(40_000);
  return {
    name: "clean-crash",
    description: "节点在 20s 时崩溃：应被移除，检测延迟 = 移除时刻 - 崩溃时刻",
    summary,
  };
}

export function staleHeartbeat(): ScenarioResult {
  const sim = new Simulation({ delaySampler: constantDelay(10) });
  sim.addNode("n1");
  sim.schedulePause("n1", 10_000, 3_500);
  sim.scheduleRestart("n1", 13_500);
  sim.scheduleCrash("n1", 16_000);
  sim.injectHeartbeat("n1", 1, 9_000, 19_500);
  sim.injectHeartbeat("n1", 1, 9_500, 19_800);
  const summary = sim.run(40_000);
  return {
    name: "stale-heartbeat",
    description: "重启后旧代次（gen1）心跳迟到到达：必须被忽略，不得阻止对 gen2 崩溃的移除",
    summary,
  };
}

function longTail(adaptive: boolean, seed: number): ScenarioResult {
  const rng = mulberry32(seed);
  const sim = new Simulation({
    delaySampler: constantDelay(10),
    delaySamplerFor: () =>
      congestedDelay(rng, {
        episodeProbability: 0.03,
        episodeMinMs: 2_000,
        episodeMaxMs: 4_000,
        congestedMinMs: 1_500,
        congestedMaxMs: 3_000,
        baseMinMs: 5,
        baseMaxMs: 80,
      }),
    detectorConfig: adaptive ? { adaptive: true } : { adaptive: false, fixedWindowMs: 300 },
  });
  for (let i = 1; i <= 8; i += 1) {
    sim.addNode(`n${i}`);
  }
  const summary = sim.run(120_000);
  return {
    name: adaptive ? "long-tail-adaptive" : "long-tail-fixed-window",
    description: adaptive
      ? "8 个节点，拥塞突发造成长尾延迟（1.5~3s），自适应等待窗口"
      : "8 个节点，拥塞突发造成长尾延迟（1.5~3s），固定 300ms 等待窗口（对照组）",
    summary,
  };
}

export function runAllScenarios(): ScenarioResult[] {
  return [
    healthyBaseline(),
    briefPause(),
    longPause(),
    cleanCrash(),
    staleHeartbeat(),
    longTail(true, 777),
    longTail(false, 777),
  ];
}
