/**
 * 故障检测度量分析仪：
 *  - 记录每次故障注入事件（发生时刻、首次怀疑时刻、最终标记时刻）；
 *  - 计算每个事件的 TTSA（首次怀疑耗时）与 MTTD（平均检测时间）；
 *  - 基于地面真值对检测结果分类（TP / TN / FP / FN），统计误报率；
 *  - 输出详尽诊断报告。
 */

export type FaultKind = 'node-crash' | 'packet-loss';

export type DetectionOutcome =
  | 'true-positive'   // 真实宕机，被最终标记故障
  | 'true-negative'   // 短暂扰动，未被标记故障（正确放行）
  | 'false-positive'  // 健康节点被误标记故障（误报）
  | 'false-negative'; // 真实宕机未被检测出（漏报）

export interface FaultEventRecord {
  readonly eventId: number;
  readonly kind: FaultKind;
  readonly nodeId: string;
  /** 故障/扰动注入时刻（协议周期）。 */
  readonly occurredAt: number;
  /** 协议首次怀疑该节点的时刻。 */
  firstSuspectedAt: number | null;
  /** 节点反驳怀疑、恢复正常的时刻。 */
  refutedAt: number | null;
  /** 协议最终标记该节点故障的时刻。 */
  confirmedFailedAt: number | null;
}

export interface EventMetrics {
  readonly eventId: number;
  readonly kind: FaultKind;
  readonly nodeId: string;
  readonly occurredAt: number;
  readonly firstSuspectedAt: number | null;
  readonly confirmedFailedAt: number | null;
  readonly refutedAt: number | null;
  /** 首次怀疑耗时（周期）；未被怀疑则为 null。 */
  readonly timeToSuspect: number | null;
  /** 检测耗时 MTTD（周期）；未被最终标记则为 null。 */
  readonly timeToDetect: number | null;
  readonly outcome: DetectionOutcome;
}

export interface AggregateMetrics {
  readonly totalEvents: number;
  readonly crashEvents: number;
  readonly disturbanceEvents: number;
  readonly truePositives: number;
  readonly trueNegatives: number;
  readonly falsePositives: number;
  readonly falseNegatives: number;
  /** 真实宕机的检出率 = TP / (TP + FN)。 */
  readonly detectionRate: number;
  /** 误报率 = FP / (FP + TN)。 */
  readonly falsePositiveRate: number;
  /** 平均检测时间（仅统计真实宕机事件，单位：协议周期）。 */
  readonly averageMttd: number | null;
  readonly minMttd: number | null;
  readonly maxMttd: number | null;
  /** 平均首次怀疑耗时（仅统计真实宕机事件）。 */
  readonly averageTimeToSuspect: number | null;
  readonly events: readonly EventMetrics[];
}

export class MetricsAnalyzer {
  private readonly records: FaultEventRecord[] = [];
  private nextEventId = 1;

  /** 记录一次故障/扰动注入，返回事件 ID。 */
  recordFaultInjection(kind: FaultKind, nodeId: string, occurredAt: number): number {
    const eventId = this.nextEventId;
    this.nextEventId += 1;
    this.records.push({
      eventId,
      kind,
      nodeId,
      occurredAt,
      firstSuspectedAt: null,
      refutedAt: null,
      confirmedFailedAt: null,
    });
    return eventId;
  }

  /** 协议首次怀疑某节点。 */
  recordSuspicion(nodeId: string, period: number): void {
    const record = this.activeRecordFor(nodeId, period);
    if (record !== null && record.firstSuspectedAt === null) {
      record.firstSuspectedAt = period;
    }
  }

  /** 被怀疑节点反驳成功。 */
  recordRefutation(nodeId: string, period: number): void {
    const record = this.activeRecordFor(nodeId, period);
    if (record !== null && record.firstSuspectedAt !== null && record.refutedAt === null) {
      record.refutedAt = period;
    }
  }

  /** 协议最终标记某节点故障。 */
  recordFailureConfirmation(nodeId: string, period: number): void {
    const record = this.activeRecordFor(nodeId, period);
    if (record !== null && record.confirmedFailedAt === null) {
      record.confirmedFailedAt = period;
    }
  }

  /** 找到该节点在指定周期处于“未决”状态的最近一次注入事件。 */
  private activeRecordFor(nodeId: string, period: number): FaultEventRecord | null {
    let active: FaultEventRecord | null = null;
    for (const record of this.records) {
      if (record.nodeId !== nodeId || record.occurredAt > period) {
        continue;
      }
      if (record.confirmedFailedAt !== null || record.refutedAt !== null) {
        continue;
      }
      active = record;
    }
    return active;
  }

  computeMetrics(): AggregateMetrics {
    const events = this.records.map((record) => MetricsAnalyzer.toEventMetrics(record));
    const crashEvents = events.filter((e) => e.kind === 'node-crash');
    const disturbanceEvents = events.filter((e) => e.kind === 'packet-loss');

    const truePositives = events.filter((e) => e.outcome === 'true-positive').length;
    const trueNegatives = events.filter((e) => e.outcome === 'true-negative').length;
    const falsePositives = events.filter((e) => e.outcome === 'false-positive').length;
    const falseNegatives = events.filter((e) => e.outcome === 'false-negative').length;

    const mttds = crashEvents
      .map((e) => e.timeToDetect)
      .filter((v): v is number => v !== null);
    const ttsas = crashEvents
      .map((e) => e.timeToSuspect)
      .filter((v): v is number => v !== null);

    const detected = truePositives + falseNegatives;
    const negativeGroundTruth = trueNegatives + falsePositives;

    return {
      totalEvents: events.length,
      crashEvents: crashEvents.length,
      disturbanceEvents: disturbanceEvents.length,
      truePositives,
      trueNegatives,
      falsePositives,
      falseNegatives,
      detectionRate: detected === 0 ? 1 : truePositives / detected,
      falsePositiveRate:
        negativeGroundTruth === 0 ? 0 : falsePositives / negativeGroundTruth,
      averageMttd: mttds.length === 0 ? null : MetricsAnalyzer.mean(mttds),
      minMttd: mttds.length === 0 ? null : Math.min(...mttds),
      maxMttd: mttds.length === 0 ? null : Math.max(...mttds),
      averageTimeToSuspect: ttsas.length === 0 ? null : MetricsAnalyzer.mean(ttsas),
      events,
    };
  }

  private static toEventMetrics(record: FaultEventRecord): EventMetrics {
    const timeToSuspect =
      record.firstSuspectedAt === null
        ? null
        : record.firstSuspectedAt - record.occurredAt;
    const timeToDetect =
      record.confirmedFailedAt === null
        ? null
        : record.confirmedFailedAt - record.occurredAt;

    let outcome: DetectionOutcome;
    if (record.kind === 'node-crash') {
      outcome = record.confirmedFailedAt === null ? 'false-negative' : 'true-positive';
    } else {
      outcome = record.confirmedFailedAt === null ? 'true-negative' : 'false-positive';
    }

    return {
      eventId: record.eventId,
      kind: record.kind,
      nodeId: record.nodeId,
      occurredAt: record.occurredAt,
      firstSuspectedAt: record.firstSuspectedAt,
      confirmedFailedAt: record.confirmedFailedAt,
      refutedAt: record.refutedAt,
      timeToSuspect,
      timeToDetect,
      outcome,
    };
  }

  private static mean(values: readonly number[]): number {
    return values.reduce((sum, v) => sum + v, 0) / values.length;
  }
}
