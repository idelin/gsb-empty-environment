import type { DetectorEvent, FaultEvent, NodeId } from './types.ts';

export type EventOutcome = 'detected' | 'transient-refuted' | 'false-marked' | 'undetected';

/** Per-fault-event diagnosis produced by the analyzer. */
export interface EventDiagnosis {
  readonly eventId: string;
  readonly type: FaultEvent['type'];
  readonly nodeId: NodeId;
  readonly occurredAt: number;
  readonly firstSuspectedAt: number | null;
  readonly markedAt: number | null;
  readonly refutedAt: number | null;
  readonly detectionLatencyCycles: number | null;
  readonly outcome: EventOutcome;
}

export interface MetricsSummary {
  readonly crashEvents: number;
  readonly detectedCrashes: number;
  readonly undetectedCrashes: number;
  readonly disturbanceEvents: number;
  readonly refutedDisturbances: number;
  readonly mttdCycles: number | null;
  readonly minDetectionLatencyCycles: number | null;
  readonly maxDetectionLatencyCycles: number | null;
  readonly truePositives: number;
  readonly falsePositives: number;
  readonly totalMarks: number;
  /** falsePositives / totalMarks, in [0, 1]. */
  readonly falsePositiveRate: number;
}

export interface AnalysisResult {
  readonly diagnoses: readonly EventDiagnosis[];
  readonly summary: MetricsSummary;
}

/**
 * Correlates ground-truth fault injections with the detector event log
 * and derives detection-quality metrics: per-event suspicion and
 * marking timestamps, mean time to detection (MTTD) over true
 * detections, and the false-positive rate over all death marks.
 */
export class MetricsAnalyzer {
  private readonly faults: readonly FaultEvent[];
  private readonly detectorLog: readonly DetectorEvent[];

  constructor(faults: readonly FaultEvent[], detectorLog: readonly DetectorEvent[]) {
    this.faults = faults;
    this.detectorLog = detectorLog;
  }

  analyze(): AnalysisResult {
    const crashedNodes = new Set(
      this.faults.filter((f) => f.type === 'node-crash').map((f) => f.nodeId),
    );
    const marks = this.detectorLog.filter((e) => e.kind === 'marked-dead');
    const falsePositives = marks.filter((e) => !crashedNodes.has(e.nodeId)).length;

    const diagnoses = this.faults.map((fault) => this.diagnose(fault));
    const detected = diagnoses.filter((d) => d.outcome === 'detected');
    const latencies = detected.map((d) => d.detectionLatencyCycles as number);
    const disturbances = diagnoses.filter((d) => d.type === 'packet-loss');

    const summary: MetricsSummary = {
      crashEvents: diagnoses.filter((d) => d.type === 'node-crash').length,
      detectedCrashes: detected.length,
      undetectedCrashes: diagnoses.filter(
        (d) => d.type === 'node-crash' && d.outcome !== 'detected',
      ).length,
      disturbanceEvents: disturbances.length,
      refutedDisturbances: disturbances.filter((d) => d.outcome === 'transient-refuted').length,
      mttdCycles:
        latencies.length === 0
          ? null
          : latencies.reduce((sum, value) => sum + value, 0) / latencies.length,
      minDetectionLatencyCycles: latencies.length === 0 ? null : Math.min(...latencies),
      maxDetectionLatencyCycles: latencies.length === 0 ? null : Math.max(...latencies),
      truePositives: marks.length - falsePositives,
      falsePositives,
      totalMarks: marks.length,
      falsePositiveRate: marks.length === 0 ? 0 : falsePositives / marks.length,
    };
    return { diagnoses, summary };
  }

  private diagnose(fault: FaultEvent): EventDiagnosis {
    const nodeEvents = this.detectorLog.filter(
      (e) => e.nodeId === fault.nodeId && e.cycle >= fault.cycle,
    );
    const firstSuspected = nodeEvents.find((e) => e.kind === 'suspected') ?? null;
    const marked = nodeEvents.find((e) => e.kind === 'marked-dead') ?? null;
    const refuted =
      nodeEvents.find(
        (e) => e.kind === 'refuted' && (firstSuspected === null || e.cycle > firstSuspected.cycle),
      ) ?? null;

    const base = {
      eventId: fault.eventId,
      type: fault.type,
      nodeId: fault.nodeId,
      occurredAt: fault.cycle,
      firstSuspectedAt: firstSuspected?.cycle ?? null,
      markedAt: marked?.cycle ?? null,
      refutedAt: refuted?.cycle ?? null,
    };

    if (fault.type === 'node-crash') {
      if (marked === null) {
        return { ...base, detectionLatencyCycles: null, outcome: 'undetected' };
      }
      return {
        ...base,
        detectionLatencyCycles: marked.cycle - fault.cycle,
        outcome: 'detected',
      };
    }
    if (marked !== null) {
      return { ...base, detectionLatencyCycles: null, outcome: 'false-marked' };
    }
    if (firstSuspected !== null && refuted !== null) {
      return { ...base, detectionLatencyCycles: null, outcome: 'transient-refuted' };
    }
    return { ...base, detectionLatencyCycles: null, outcome: 'undetected' };
  }
}
