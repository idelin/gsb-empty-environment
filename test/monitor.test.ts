import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ExposureMonitor } from '../src/monitor.ts';

const GROUP_LIMIT = 10_000_000;
const WARNING_RATIO = 0.7;
const WARNING_LINE = GROUP_LIMIT * WARNING_RATIO;

/** 验收基线：集团授信 1000 万 CNY，协议有效期 [0, 50)，子公司 A 于 t=10 借款 600 万。 */
function buildBaseline(): ExposureMonitor {
  const monitor = new ExposureMonitor({ baseCurrency: 'CNY' });
  monitor.recordCreditLine(
    { groupId: 'G', limitAmount: GROUP_LIMIT, currency: 'CNY', warningRatio: WARNING_RATIO },
    0,
    50,
    0,
  );
  monitor.recordLoan({ subsidiary: 'A', amount: 6_000_000, currency: 'CNY' }, 10, Infinity, 10);
  return monitor;
}

test('验收：时间30追溯上调A的历史借款为700万，重现时间30总敞口触碰警戒线并告警', () => {
  const monitor = buildBaseline();

  // 修正前，t=29 时系统认知的 t=30 状态：敞口 600 万，未触碰预警线
  const before = monitor.evaluate(30, 29);
  assert.equal(before.totalExposure, 6_000_000);
  assert.equal(before.warningLine, WARNING_LINE);
  assert.equal(before.breached, false);
  assert.equal(before.alerts.length, 0);

  // t=30 追溯修正：A 自 t=10 起的历史借款上调为 700 万
  monitor.recordLoan({ subsidiary: 'A', amount: 7_000_000, currency: 'CNY' }, 10, Infinity, 30);

  // 重现时间 30：总敞口 700 万，恰好触碰预警线 700 万，输出告警
  const replay = monitor.evaluate(30, 30);
  assert.equal(replay.totalExposure, 7_000_000);
  assert.equal(replay.limit, GROUP_LIMIT);
  assert.equal(replay.warningLine, WARNING_LINE);
  assert.equal(replay.breached, true);
  const breachAlerts = replay.alerts.filter((a) => a.code === 'WARNING_LINE_BREACHED');
  assert.equal(breachAlerts.length, 1);
  assert.match(breachAlerts[0].message, /7000000\.00/);

  // 双时态：修正不影响"当时系统所知"的历史视图
  const historicalView = monitor.evaluate(30, 29);
  assert.equal(historicalView.totalExposure, 6_000_000);
  assert.equal(historicalView.breached, false);
});

test('多币种：汇率波动影响敞口折算，历史时点取当时生效的汇率', () => {
  const monitor = buildBaseline();
  monitor.recordLoan({ subsidiary: 'B', amount: 200_000, currency: 'USD' }, 12, Infinity, 12);
  monitor.recordFxRate({ from: 'USD', to: 'CNY', rate: 7.0 }, 0, 0);
  monitor.recordFxRate({ from: 'USD', to: 'CNY', rate: 7.25 }, 20, 20);

  // t=15：USD/CNY = 7.0，B 敞口折算 140 万，合计 740 万
  const at15 = monitor.evaluate(15, 15);
  const itemB15 = at15.items.find((i) => i.subsidiary === 'B');
  assert.equal(itemB15?.fxRate, 7.0);
  assert.equal(itemB15?.converted, 1_400_000);
  assert.equal(at15.totalExposure, 7_400_000);
  assert.equal(at15.breached, true);

  // t=25：汇率升至 7.25，B 敞口折算 145 万，合计 745 万
  const at25 = monitor.evaluate(25, 25);
  const itemB25 = at25.items.find((i) => i.subsidiary === 'B');
  assert.equal(itemB25?.fxRate, 7.25);
  assert.equal(itemB25?.converted, 1_450_000);
  assert.equal(at25.totalExposure, 7_450_000);

  // 双时态重现：t=25 回看 t=15，仍使用当时生效的 7.0 汇率
  const replay15 = monitor.evaluate(15, 25);
  assert.equal(replay15.items.find((i) => i.subsidiary === 'B')?.converted, 1_400_000);
});

test('协议有效期：协议到期后评估输出 AGREEMENT_INACTIVE 告警', () => {
  const monitor = buildBaseline();
  const report = monitor.evaluate(55, 55);
  assert.equal(report.limit, null);
  assert.equal(report.breached, false);
  assert.ok(report.alerts.some((a) => a.code === 'AGREEMENT_INACTIVE'));
});

test('担保物价值变动引发质押率重算并留下追溯记录', () => {
  const monitor = buildBaseline();
  monitor.recordCollateral({ owner: 'A', value: 8_000_000, currency: 'CNY' }, 10, 10);

  // t=12 首次评估：质押率 600/800 = 75%
  monitor.evaluate(12, 12);
  // t=25 担保物价值重估下跌至 650 万
  monitor.recordCollateral({ owner: 'A', value: 6_500_000, currency: 'CNY' }, 25, 25);
  // t=26 评估触发重算：质押率 600/650 ≈ 92.31%
  monitor.evaluate(26, 26);

  const trace = monitor.getPledgeTrace().filter((e) => e.owner === 'A');
  assert.equal(trace.length, 2);
  assert.ok(Math.abs(trace[0].pledgeRatio - 0.75) < 1e-9);
  assert.deepEqual([...trace[0].causes], ['首次评估']);
  assert.ok(Math.abs(trace[1].pledgeRatio - 6_000_000 / 6_500_000) < 1e-9);
  assert.equal(trace[1].previousRatio, 0.75);
  assert.deepEqual([...trace[1].causes], ['担保物价值变动']);

  // t=30 追溯上调借款至 700 万后重算：700/650 ≈ 107.69%，超过 100% 上限并告警
  monitor.recordLoan({ subsidiary: 'A', amount: 7_000_000, currency: 'CNY' }, 10, Infinity, 30);
  const replay = monitor.evaluate(30, 30);
  const pledge = replay.pledges.find((p) => p.owner === 'A');
  assert.ok(pledge !== undefined && pledge.pledgeRatio > 1);
  assert.ok(replay.alerts.some((a) => a.code === 'PLEDGE_RATIO_EXCEEDED'));

  const traceAfter = monitor.getPledgeTrace().filter((e) => e.owner === 'A');
  assert.equal(traceAfter.length, 3);
  assert.deepEqual([...traceAfter[2].causes], ['借款敞口变动']);
});

test('缺失汇率时输出 FX_RATE_MISSING 且该笔敞口不计入总额', () => {
  const monitor = buildBaseline();
  monitor.recordLoan({ subsidiary: 'C', amount: 100_000, currency: 'EUR' }, 12, Infinity, 12);
  const report = monitor.evaluate(15, 15);
  assert.equal(report.totalExposure, 6_000_000);
  assert.ok(report.alerts.some((a) => a.code === 'FX_RATE_MISSING'));
  const itemC = report.items.find((i) => i.subsidiary === 'C');
  assert.equal(itemC?.converted, null);
});
