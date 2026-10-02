import { SwimCluster, type SwimEventSink } from './swim.ts';
import { MetricsAnalyzer, type AggregateMetrics, type FaultKind } from './metrics.ts';

/**
 * 验收场景：向 12 节点集群注入 10 次真实节点宕机 + 5 次短暂网络丢包扰动，
 * 驱动 SWIM 仿真并由度量分析仪采集指标。
 */

export interface ScheduledFault {
  readonly period: number;
  readonly kind: FaultKind;
  readonly nodeId: string;
  /** 仅 packet-loss 使用：扰动持续周期数。 */
  readonly duration?: number;
}

export interface ScenarioConfig {
  readonly name: string;
  readonly nodeCount: number;
  readonly seed: number;
  readonly suspicionTimeout: number;
  readonly indirectProbes: number;
  readonly packetLossDuration: number;
  readonly faults: readonly ScheduledFault[];
  readonly totalPeriods: number;
}

/** 标准验收场景：10 次宕机 + 5 次扰动，确定性种子保证可复现。 */
export function standardScenario(seed = 6): ScenarioConfig {
  const faults: ScheduledFault[] = [];
  // 10 次真实宕机：node-1 .. node-10，每隔 4 个周期注入一次。
  for (let i = 0; i < 10; i += 1) {
    faults.push({
      period: 4 + i * 4,
      kind: 'node-crash',
      nodeId: `node-${i + 1}`,
    });
  }
  // 5 次短暂丢包扰动：仅作用于永不宕机的 node-11 / node-12，持续 1 周期。
  const disturbancePeriods = [6, 14, 22, 30, 38];
  disturbancePeriods.forEach((period, i) => {
    faults.push({
      period,
      kind: 'packet-loss',
      nodeId: i % 2 === 0 ? 'node-11' : 'node-12',
      duration: 1,
    });
  });
  faults.sort((a, b) => a.period - b.period);

  return {
    name: '12 节点集群 / 10 次宕机 + 5 次短暂丢包扰动',
    nodeCount: 12,
    seed,
    suspicionTimeout: 2,
    indirectProbes: 3,
    packetLossDuration: 1,
    faults,
    totalPeriods: 48,
  };
}

export interface ScenarioResult {
  readonly metrics: AggregateMetrics;
  readonly analyzer: MetricsAnalyzer;
  readonly cluster: SwimCluster;
}

/** 运行场景：按调度注入故障，逐周期推进协议并采集指标。 */
export function runScenario(config: ScenarioConfig): ScenarioResult {
  const analyzer = new MetricsAnalyzer();

  const sink: SwimEventSink = {
    onSuspect: (nodeId, period) => analyzer.recordSuspicion(nodeId, period),
    onRefute: (nodeId, period) => analyzer.recordRefutation(nodeId, period),
    onConfirmFailed: (nodeId, period) =>
      analyzer.recordFailureConfirmation(nodeId, period),
  };

  const cluster = new SwimCluster(
    config.nodeCount,
    config.seed,
    {
      suspicionTimeout: config.suspicionTimeout,
      indirectProbes: config.indirectProbes,
    },
    sink,
  );

  const faultsByPeriod = new Map<number, ScheduledFault[]>();
  for (const fault of config.faults) {
    const list = faultsByPeriod.get(fault.period) ?? [];
    list.push(fault);
    faultsByPeriod.set(fault.period, list);
  }

  for (let period = 0; period < config.totalPeriods; period += 1) {
    // 周期开始时注入故障（地面真值），并通知分析仪。
    for (const fault of faultsByPeriod.get(period) ?? []) {
      if (fault.kind === 'node-crash') {
        cluster.crash(fault.nodeId);
      } else {
        cluster.injectPacketLoss(fault.nodeId, fault.duration ?? config.packetLossDuration);
      }
      analyzer.recordFaultInjection(fault.kind, fault.nodeId, period);
    }
    cluster.runPeriod(period);
  }

  return { metrics: analyzer.computeMetrics(), analyzer, cluster };
}
