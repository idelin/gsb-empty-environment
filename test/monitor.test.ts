import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LsmDiagnosticsMonitor, type GetSample } from '../src/monitor.ts';

const healthy: GetSample = { sstablesRead: 1, bloomChecks: 5, bloomNegatives: 4, falsePositives: 0, found: true };
const highReadAmp: GetSample = { sstablesRead: 5, bloomChecks: 0, bloomNegatives: 0, falsePositives: 0, found: true };
const highFp: GetSample = { sstablesRead: 2, bloomChecks: 5, bloomNegatives: 2, falsePositives: 2, found: true };

function feed(monitor: LsmDiagnosticsMonitor, sample: GetSample, count: number) {
  const alerts = [];
  for (let i = 0; i < count; i++) alerts.push(...monitor.record(sample));
  return alerts;
}

test('健康负载不触发告警', () => {
  const monitor = new LsmDiagnosticsMonitor({ windowSize: 100, minSamples: 20 });
  const alerts = feed(monitor, healthy, 100);
  assert.equal(alerts.length, 0);
  const snap = monitor.snapshot();
  assert.ok(snap.avgReadAmplification <= 1.2);
  assert.equal(snap.falsePositiveRate, 0);
});

test('读放大越限触发 Compaction 建议告警（边沿触发，仅一次）', () => {
  const monitor = new LsmDiagnosticsMonitor({ windowSize: 100, minSamples: 20, readAmpWarn: 2.0 });
  feed(monitor, healthy, 50);
  const alerts = feed(monitor, highReadAmp, 80);
  const readAmpAlerts = alerts.filter((a) => a.code === 'HIGH_READ_AMPLIFICATION');
  assert.equal(readAmpAlerts.length, 1);
  assert.match(readAmpAlerts[0].suggestion, /Compaction/);
  assert.match(readAmpAlerts[0].suggestion, /读延迟/);
  assert.ok(readAmpAlerts[0].value > 2.0);
});

test('误判率越限触发重建 Filter 建议告警', () => {
  const monitor = new LsmDiagnosticsMonitor({ windowSize: 100, minSamples: 20, fpRateWarn: 0.05 });
  const alerts = feed(monitor, highFp, 60);
  const fpAlerts = alerts.filter((a) => a.code === 'HIGH_BLOOM_FP_RATE');
  assert.equal(fpAlerts.length, 1);
  assert.match(fpAlerts[0].suggestion, /重建 Bloom Filter/);
  assert.ok(fpAlerts[0].value > 0.05);
});

test('样本不足时不评估', () => {
  const monitor = new LsmDiagnosticsMonitor({ windowSize: 100, minSamples: 50 });
  const alerts = feed(monitor, highReadAmp, 30);
  assert.equal(alerts.length, 0);
});

test('滑动窗口：恢复健康后指标回落', () => {
  const monitor = new LsmDiagnosticsMonitor({ windowSize: 50, minSamples: 20, readAmpWarn: 2.0 });
  feed(monitor, highReadAmp, 50);
  assert.ok(monitor.snapshot().avgReadAmplification > 2.0);
  feed(monitor, healthy, 50);
  const snap = monitor.snapshot();
  assert.ok(snap.avgReadAmplification <= 1.0);
  assert.equal(monitor.evaluate().length, 0);
});
