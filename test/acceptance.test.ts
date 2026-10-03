import assert from 'node:assert/strict';
import { describe, test } from 'node:test';
import { standardScenario } from '../src/faultInjection.ts';
import { MetricsAnalyzer, type AnalysisResult } from '../src/metrics.ts';
import { SwimCluster, SwimDetector, simulate } from '../src/swim.ts';

function runAcceptanceScenario(): AnalysisResult {
  const scenario = standardScenario();
  const cluster = new SwimCluster(scenario.clusterSize);
  for (const event of scenario.events) {
    cluster.applyFault(event);
  }
  const detector = new SwimDetector(cluster);
  const log = simulate(cluster, detector, scenario.totalCycles);
  return new MetricsAnalyzer(scenario.events, log).analyze();
}

describe('acceptance: 10 node crashes + 5 transient packet-loss disturbances', () => {
  const result = runAcceptanceScenario();
  const { diagnoses, summary } = result;

  test('scenario injects exactly 10 crashes and 5 disturbances', () => {
    assert.equal(summary.crashEvents, 10);
    assert.equal(summary.disturbanceEvents, 5);
  });

  test('every real crash is detected', () => {
    assert.equal(summary.detectedCrashes, 10);
    assert.equal(summary.undetectedCrashes, 0);
  });

  test('MTTD averages 2.4 cycles', () => {
    assert.equal(summary.mttdCycles, 2.4);
    assert.equal(summary.minDetectionLatencyCycles, 2);
    assert.equal(summary.maxDetectionLatencyCycles, 3);
  });

  test('detection latencies are 6x2 cycles and 4x3 cycles', () => {
    const latencies = diagnoses
      .filter((d) => d.type === 'node-crash')
      .map((d) => d.detectionLatencyCycles)
      .sort();
    assert.deepEqual(latencies, [2, 2, 2, 2, 2, 2, 3, 3, 3, 3]);
  });

  test('false-positive rate is 0%', () => {
    assert.equal(summary.falsePositives, 0);
    assert.equal(summary.falsePositiveRate, 0);
    assert.equal(summary.totalMarks, 10);
    assert.equal(summary.truePositives, 10);
  });

  test('all 5 transient disturbances are suspected then refuted, never marked', () => {
    const disturbances = diagnoses.filter((d) => d.type === 'packet-loss');
    assert.equal(disturbances.length, 5);
    for (const d of disturbances) {
      assert.equal(d.outcome, 'transient-refuted');
      assert.notEqual(d.firstSuspectedAt, null);
      assert.equal(d.markedAt, null);
      assert.equal(d.refutedAt, (d.firstSuspectedAt as number) + 1);
    }
    assert.equal(summary.refutedDisturbances, 5);
  });

  test('every event records occurrence, first-suspicion and final-mark timestamps', () => {
    for (const d of diagnoses) {
      assert.equal(typeof d.occurredAt, 'number');
      assert.notEqual(d.firstSuspectedAt, null);
      if (d.type === 'node-crash') {
        assert.notEqual(d.markedAt, null);
        assert.ok((d.markedAt as number) > d.occurredAt);
      }
    }
  });
});
