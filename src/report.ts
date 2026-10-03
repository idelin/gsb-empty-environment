import type { AnalysisResult, EventDiagnosis } from './metrics.ts';
import type { FaultScenario } from './faultInjection.ts';

function fmtCycle(value: number | null): string {
  return value === null ? '-' : String(value);
}

function fmtRow(columns: string[]): string {
  return `| ${columns.join(' | ')} |`;
}

function diagnosisRow(d: EventDiagnosis): string {
  return fmtRow([
    d.eventId.padEnd(8),
    d.type.padEnd(11),
    d.nodeId,
    String(d.occurredAt).padStart(4),
    fmtCycle(d.firstSuspectedAt).padStart(4),
    fmtCycle(d.markedAt).padStart(4),
    fmtCycle(d.refutedAt).padStart(4),
    fmtCycle(d.detectionLatencyCycles).padStart(4),
    d.outcome,
  ]);
}

/** Renders the full diagnostic report as plain text. */
export function renderReport(scenario: FaultScenario, result: AnalysisResult): string {
  const { diagnoses, summary } = result;
  const lines: string[] = [];

  lines.push('================================================================');
  lines.push(' SWIM Failure-Detector Diagnostics Report');
  lines.push('================================================================');
  lines.push('');
  lines.push('Scenario');
  lines.push('--------');
  lines.push(`  Cluster size ............ ${scenario.clusterSize} members`);
  lines.push(`  Observation window ...... ${scenario.totalCycles} protocol cycles`);
  lines.push(`  Injected node crashes ... ${summary.crashEvents}`);
  lines.push(`  Injected disturbances ... ${summary.disturbanceEvents} (transient packet loss)`);
  lines.push('');
  lines.push('Per-Event Timeline (all cycles are protocol cycles)');
  lines.push('---------------------------------------------------');
  lines.push(
    fmtRow([
      'event   ',
      'type       ',
      'node    ',
      'occ ',
      'susp',
      'mark',
      'reft',
      'mttd',
      'outcome',
    ]),
  );
  lines.push(
    fmtRow([
      '--------',
      '-----------',
      '--------',
      '----',
      '----',
      '----',
      '----',
      '----',
      '----------------',
    ]),
  );
  const sorted = [...diagnoses].sort((a, b) => a.occurredAt - b.occurredAt);
  for (const diagnosis of sorted) {
    lines.push(diagnosisRow(diagnosis));
  }
  lines.push('');
  lines.push('Aggregate Metrics');
  lines.push('-----------------');
  lines.push(
    `  MTTD (mean) ............. ${
      summary.mttdCycles === null ? 'n/a' : summary.mttdCycles.toFixed(2)
    } cycles over ${summary.detectedCrashes} detections`,
  );
  lines.push(`  Detection latency min ... ${fmtCycle(summary.minDetectionLatencyCycles)} cycles`);
  lines.push(`  Detection latency max ... ${fmtCycle(summary.maxDetectionLatencyCycles)} cycles`);
  lines.push(`  Undetected crashes ...... ${summary.undetectedCrashes}`);
  lines.push(
    `  Transient suspicions .... ${summary.refutedDisturbances}/${summary.disturbanceEvents} refuted`,
  );
  lines.push(
    `  Death marks ............. ${summary.totalMarks} ` +
      `(${summary.truePositives} true, ${summary.falsePositives} false)`,
  );
  lines.push(
    `  False-positive rate ..... ${(summary.falsePositiveRate * 100).toFixed(1)}%`,
  );
  lines.push('');
  lines.push('Conclusion');
  lines.push('----------');
  const robust = summary.undetectedCrashes === 0 && summary.falsePositives === 0;
  lines.push(
    robust
      ? '  The detector identified every real crash and never marked a'
      : '  The detector deviated from ground truth:',
  );
  lines.push(
    robust
      ? '  healthy member dead. All transient packet-loss suspicions were'
      : `  ${summary.undetectedCrashes} undetected crash(es),`,
  );
  lines.push(
    robust
      ? '  refuted within one cycle. Protocol robustness: CONFIRMED.'
      : `  ${summary.falsePositives} false death mark(s). Robustness: VIOLATED.`,
  );
  return lines.join('\n');
}
