import { Simulation } from "./sim.ts";
import type { MonitorConfig } from "./monitor.ts";
import { lognormal, mulberry32, pareto, type Rng } from "./rng.ts";
import type { Heartbeat } from "./types.ts";

export interface Scenario {
  name: string;
  description: string;
  create(): { sim: Simulation; durationMs: number };
}

export function defaultMonitorConfig(overrides: Partial<MonitorConfig> = {}): MonitorConfig {
  return {
    heartbeatIntervalMs: 100,
    suspectThreshold: 3,
    windowCapacity: 16,
    marginK: 3,
    minSuspectTimeoutMs: 150,
    maxSuspectTimeoutMs: 2000,
    ...overrides,
  };
}

function makeSim(
  seed: number,
  delaySampler: (rng: Rng) => (now: number) => number,
  monitor: Partial<MonitorConfig> = {},
): Simulation {
  const rng = mulberry32(seed);
  return new Simulation({
    monitor: defaultMonitorConfig(monitor),
    tickMs: 25,
    delaySampler: delaySampler(rng),
  });
}

const mildDelay = (rng: Rng) => () => lognormal(rng, 20, 0.25);

export function longTailDelaySampler(seed: number): (now: number) => number {
  const rng = mulberry32(seed);
  let congestedUntil = -1;
  return (now: number) => {
    if (now > congestedUntil && rng() < 0.01) {
      congestedUntil = now + 1_000 + rng() * 2_000;
    }
    if (now < congestedUntil) {
      return Math.min(pareto(rng, 400, 1.5), 3_000);
    }
    return lognormal(rng, 20, 0.25);
  };
}

export function baseline(): Scenario {
  return {
    name: "baseline",
    description: "单节点，对数正态小抖动，无任何故障",
    create() {
      const sim = makeSim(1, mildDelay);
      sim.addNode("n1");
      return { sim, durationMs: 30_000 };
    },
  };
}

export function pauseBursts(): Scenario {
  return {
    name: "pause-bursts",
    description: "节点三次暂停：50ms（不怀疑）、250ms（怀疑后恢复）、2000ms（误删后重加入）",
    create() {
      const sim = makeSim(2, mildDelay);
      sim.addNode("n1");
      sim.schedule({ kind: "pause-node", at: 9_000, nodeId: "n1" });
      sim.schedule({ kind: "resume-node", at: 9_050, nodeId: "n1" });
      sim.schedule({ kind: "pause-node", at: 10_000, nodeId: "n1" });
      sim.schedule({ kind: "resume-node", at: 10_250, nodeId: "n1" });
      sim.schedule({ kind: "pause-node", at: 14_000, nodeId: "n1" });
      sim.schedule({ kind: "resume-node", at: 16_000, nodeId: "n1" });
      return { sim, durationMs: 20_000 };
    },
  };
}

export function longTailDelay(): Scenario {
  return {
    name: "long-tail-delay",
    description: "间歇性网络拥塞：拥塞期延迟飙升至数百~数千 ms（长尾），考验自适应窗口",
    create() {
      const sim = makeSim(3, () => longTailDelaySampler(3));
      sim.addNode("n1");
      return { sim, durationMs: 60_000 };
    },
  };
}

export function staleHeartbeats(): Scenario {
  return {
    name: "stale-heartbeats",
    description: "崩溃后快速重启（代次+1），旧代次心跳迟到；随后永久崩溃，崩溃前的旧心跳迟到",
    create() {
      const sim = makeSim(4, mildDelay);
      sim.addNode("n1");
      sim.schedule({ kind: "crash-node", at: 5_000, nodeId: "n1" });
      sim.schedule({ kind: "restart-node", at: 5_300, nodeId: "n1" });
      const staleEpoch1a: Heartbeat = { nodeId: "n1", epoch: 1, seq: 48, sentAt: 4_900 };
      const staleEpoch1b: Heartbeat = { nodeId: "n1", epoch: 1, seq: 49, sentAt: 4_950 };
      sim.schedule({ kind: "deliver-heartbeat", at: 6_000, hb: staleEpoch1a });
      sim.schedule({ kind: "deliver-heartbeat", at: 7_000, hb: staleEpoch1b });
      sim.schedule({ kind: "crash-node", at: 12_000, nodeId: "n1" });
      const staleEpoch2a: Heartbeat = { nodeId: "n1", epoch: 2, seq: 65, sentAt: 11_800 };
      const staleEpoch2b: Heartbeat = { nodeId: "n1", epoch: 2, seq: 66, sentAt: 11_900 };
      sim.schedule({ kind: "deliver-heartbeat", at: 13_000, hb: staleEpoch2a });
      sim.schedule({ kind: "deliver-heartbeat", at: 14_000, hb: staleEpoch2b });
      return { sim, durationMs: 20_000 };
    },
  };
}

export function realCrash(): Scenario {
  return {
    name: "real-crash",
    description: "节点在 10s 永久崩溃，测量从崩溃到移除的检测延迟",
    create() {
      const sim = makeSim(5, mildDelay);
      sim.addNode("n1");
      sim.schedule({ kind: "crash-node", at: 10_000, nodeId: "n1" });
      return { sim, durationMs: 20_000 };
    },
  };
}

export const scenarios: Scenario[] = [
  baseline(),
  pauseBursts(),
  longTailDelay(),
  staleHeartbeats(),
  realCrash(),
];
