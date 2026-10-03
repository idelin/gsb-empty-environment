import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { MetricsAnalyzer } from '../src/metrics.ts';
import type { DetectorEvent, FaultEvent } from '../src/types.ts';

function crash(eventId: string, nodeId: string, cycle: number): FaultEvent {
  return { eventId, type: 'node-crash', nodeId, cycle, phase: 0 };
}

function loss(eventId: string, nodeId: string, cycle: number): FaultEvent {
  return { eventId, type: 'packet-loss', nodeId, cycle, phase: 0 };
}

describe('MetricsAnalyzer', () => {
  test('computes MTTD as the mean of per-event detection latencies', () => {
    const faults = [crash('c1', 'n1', 10), crash('c2', 'n2', 20), crash('c3', 'n3', 30)];
    const log: DetectorEvent[] = [
      { cycle: 10, nodeId: 'n1', kind: 'suspected' },
      { cycle: 12, nodeId: 'n1', kind: 'marked-dead' },
      { cycle: 21, nodeId: 'n2', kind: 'suspected' },
      { cycle: 23, nodeId: 'n2', kind: 'marked-dead' },
      { cycle: 30, nodeId: 'n3', kind: 'suspected' },
      { cycle: 34, nodeId: 'n3', kind: 'marked-dead' },
    ];
    const { diagnoses, summary } = new MetricsAnalyzer(faults, log).analyze();

    assert.deepEqual(
      diagnoses.map((d) => [d.occurredAt, d.firstSuspectedAt, d.markedAt, d.detectionLatencyCycles]),
      [
        [10, 10, 12, 2],
        [20, 21, 23, 3],
        [30, 30, 34, 4],
      ],
    );
    assert.equal(summary.mttdCycles, 3);
    assert.equal(summary.minDetectionLatencyCycles, 2);
    assert.equal(summary.maxDetectionLatencyCycles, 4);
    assert.equal(summary.detectedCrashes, 3);
    assert.equal(summary.falsePositiveRate, 0);
  });

  test('flags death marks on healthy nodes as false positives', () => {
    const faults = [crash('c1', 'n1', 10), loss('l1', 'n9', 15)];
    const log: DetectorEvent[] = [
      { cycle: 10, nodeId: 'n1', kind: 'suspected' },
      { cycle: 12, nodeId: 'n1', kind: 'marked-dead' },
      { cycle: 15, nodeId: 'n9', kind: 'suspected' },
      { cycle: 18, nodeId: 'n9', kind: 'marked-dead' },
    ];
    const { diagnoses, summary } = new MetricsAnalyzer(faults, log).analyze();

    assert.equal(summary.truePositives, 1);
    assert.equal(summary.falsePositives, 1);
    assert.equal(summary.totalMarks, 2);
    assert.equal(summary.falsePositiveRate, 0.5);
    assert.equal(diagnoses[1]?.outcome, 'false-marked');
  });

  test('counts crashes that are never marked as undetected', () => {
    const faults = [crash('c1', 'n1', 10)];
    const log: DetectorEvent[] = [{ cycle: 10, nodeId: 'n1', kind: 'suspected' }];
    const { summary } = new MetricsAnalyzer(faults, log).analyze();

    assert.equal(summary.undetectedCrashes, 1);
    assert.equal(summary.mttdCycles, null);
  });

  test('recognizes a refuted transient suspicion as correct behavior', () => {
    const faults = [loss('l1', 'n5', 7)];
    const log: DetectorEvent[] = [
      { cycle: 7, nodeId: 'n5', kind: 'suspected' },
      { cycle: 8, nodeId: 'n5', kind: 'refuted' },
    ];
    const { diagnoses, summary } = new MetricsAnalyzer(faults, log).analyze();

    assert.equal(diagnoses[0]?.outcome, 'transient-refuted');
    assert.equal(diagnoses[0]?.refutedAt, 8);
    assert.equal(summary.refutedDisturbances, 1);
    assert.equal(summary.falsePositiveRate, 0);
  });

  test('ignores detector events that predate the fault occurrence', () => {
    const faults = [crash('c1', 'n1', 10)];
    const log: DetectorEvent[] = [
      { cycle: 3, nodeId: 'n1', kind: 'suspected' },
      { cycle: 4, nodeId: 'n1', kind: 'refuted' },
      { cycle: 10, nodeId: 'n1', kind: 'suspected' },
      { cycle: 12, nodeId: 'n1', kind: 'marked-dead' },
    ];
    const { diagnoses } = new MetricsAnalyzer(faults, log).analyze();

    assert.equal(diagnoses[0]?.firstSuspectedAt, 10);
    assert.equal(diagnoses[0]?.detectionLatencyCycles, 2);
  });
});
