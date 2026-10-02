import { test } from 'node:test';
import assert from 'node:assert/strict';

import { MetricsAnalyzer } from '../src/metrics.ts';
import { runScenario, standardScenario } from '../src/scenario.ts';
import { generateReport } from '../src/report.ts';

test('验收场景: 10 次宕机 + 5 次扰动, MTTD 平均 2.4 周期, 误报率 0%', () => {
  const { metrics } = runScenario(standardScenario());

  assert.equal(metrics.totalEvents, 15);
  assert.equal(metrics.crashEvents, 10);
  assert.equal(metrics.disturbanceEvents, 5);

  // 判定矩阵: 全部宕机检出, 全部扰动正确放行。
  assert.equal(metrics.truePositives, 10);
  assert.equal(metrics.trueNegatives, 5);
  assert.equal(metrics.falsePositives, 0);
  assert.equal(metrics.falseNegatives, 0);

  // 核心验收指标。
  assert.equal(metrics.detectionRate, 1);
  assert.equal(metrics.falsePositiveRate, 0);
  assert.ok(metrics.averageMttd !== null);
  assert.ok(
    Math.abs(metrics.averageMttd - 2.4) < 1e-9,
    `expected average MTTD 2.4, got ${metrics.averageMttd}`,
  );
  assert.equal(metrics.minMttd, 2);
  assert.equal(metrics.maxMttd, 3);
});

test('每个宕机事件记录发生/首疑/标记时刻, MTTD = 标记 - 发生', () => {
  const { metrics } = runScenario(standardScenario());

  for (const event of metrics.events) {
    if (event.kind !== 'node-crash') continue;
    assert.ok(event.firstSuspectedAt !== null, `event #${event.eventId} never suspected`);
    assert.ok(event.confirmedFailedAt !== null, `event #${event.eventId} never confirmed`);
    assert.ok(event.firstSuspectedAt >= event.occurredAt);
    assert.ok(event.confirmedFailedAt > event.firstSuspectedAt);
    assert.equal(event.timeToDetect, event.confirmedFailedAt - event.occurredAt);
    assert.equal(event.timeToSuspect, event.firstSuspectedAt - event.occurredAt);
    assert.equal(event.outcome, 'true-positive');
  }
});

test('短暂丢包扰动只触发怀疑与反驳, 不会升级为误报', () => {
  const { metrics } = runScenario(standardScenario());

  const disturbances = metrics.events.filter((e) => e.kind === 'packet-loss');
  assert.equal(disturbances.length, 5);
  for (const event of disturbances) {
    assert.equal(event.confirmedFailedAt, null);
    assert.equal(event.timeToDetect, null);
    assert.equal(event.outcome, 'true-negative');
  }
  // 至少部分扰动曾引发怀疑并被成功反驳, 证明怀疑-反驳机制真实运作。
  const refuted = disturbances.filter((e) => e.refutedAt !== null);
  assert.ok(refuted.length > 0, 'expected at least one disturbance to be suspected then refuted');
});

test('相同种子仿真结果完全可复现', () => {
  const first = runScenario(standardScenario(123)).metrics;
  const second = runScenario(standardScenario(123)).metrics;
  assert.deepEqual(second, first);
});

test('分析仪独立单元: 手工事件流的 MTTD 与误报率计算', () => {
  const analyzer = new MetricsAnalyzer();

  // 两次真实宕机: MTTD 分别为 2 与 4。
  analyzer.recordFaultInjection('node-crash', 'node-a', 10);
  analyzer.recordSuspicion('node-a', 11);
  analyzer.recordFailureConfirmation('node-a', 12);

  analyzer.recordFaultInjection('node-crash', 'node-b', 20);
  analyzer.recordSuspicion('node-b', 22);
  analyzer.recordFailureConfirmation('node-b', 24);

  // 一次扰动被正确放行 (TN), 一次扰动被误标 (FP)。
  analyzer.recordFaultInjection('packet-loss', 'node-c', 30);
  analyzer.recordSuspicion('node-c', 30);
  analyzer.recordRefutation('node-c', 31);

  analyzer.recordFaultInjection('packet-loss', 'node-d', 40);
  analyzer.recordSuspicion('node-d', 40);
  analyzer.recordFailureConfirmation('node-d', 42);

  const metrics = analyzer.computeMetrics();
  assert.equal(metrics.averageMttd, 3);
  assert.equal(metrics.truePositives, 2);
  assert.equal(metrics.trueNegatives, 1);
  assert.equal(metrics.falsePositives, 1);
  assert.equal(metrics.falsePositiveRate, 0.5);
  assert.equal(metrics.detectionRate, 1);

  const [crashA, , disturbanceC, disturbanceD] = metrics.events;
  assert.equal(crashA.timeToSuspect, 1);
  assert.equal(crashA.timeToDetect, 2);
  assert.equal(disturbanceC.outcome, 'true-negative');
  assert.equal(disturbanceD.outcome, 'false-positive');
});

test('漏报场景: 宕机未被确认时计入 FN 且拉低检出率', () => {
  const analyzer = new MetricsAnalyzer();
  analyzer.recordFaultInjection('node-crash', 'node-a', 5);
  analyzer.recordFaultInjection('node-crash', 'node-b', 6);
  analyzer.recordSuspicion('node-b', 6);
  analyzer.recordFailureConfirmation('node-b', 8);

  const metrics = analyzer.computeMetrics();
  assert.equal(metrics.falseNegatives, 1);
  assert.equal(metrics.truePositives, 1);
  assert.equal(metrics.detectionRate, 0.5);
  assert.equal(metrics.averageMttd, 2);
});

test('诊断报告包含关键章节与验收指标', () => {
  const scenario = standardScenario();
  const { metrics } = runScenario(scenario);
  const report = generateReport(metrics, scenario.name);

  assert.match(report, /SWIM 故障检测诊断报告/);
  assert.match(report, /\[事件明细\]/);
  assert.match(report, /\[聚合指标\]/);
  assert.match(report, /\[检测时延\]/);
  assert.match(report, /\[结论\]/);
  assert.match(report, /误报率 \(FP\/\(FP\+TN\)\) : 0\.0%/);
  assert.match(report, /平均检测时间 MTTD   : 2\.40/);
  assert.match(report, /TP=10 TN=5 FP=0 FN=0/);
});
