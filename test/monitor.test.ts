import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ExposureMonitor } from '../src/monitor.ts';

/** 验收场景：构建到 t=30（含追溯修正）的监控器。 */
function buildAcceptanceScenario(): ExposureMonitor {
  const m = new ExposureMonitor(0.8);
  m.setGroupLimit({ limit: 10_000_000, currency: 'CNY', warningRatio: 0.9 }, 0, 0);
  m.addAgreement({ subsidiaryId: 'A', limit: 8_000_000, currency: 'CNY' }, 0, 0);
  m.addAgreement({ subsidiaryId: 'B', limit: 5_000_000, currency: 'CNY' }, 0, 0);
  m.recordFxRate({ from: 'USD', to: 'CNY', rate: 7.0 }, 0, 0);
  m.recordDrawdown({ subsidiaryId: 'A', amount: 6_000_000, currency: 'CNY' }, 10, 10);
  m.revalueCollateral({ collateralId: 'COL-A', subsidiaryId: 'A', value: 8_000_000, currency: 'CNY' }, 10, 10);
  m.recordDrawdown({ subsidiaryId: 'B', amount: 400_000, currency: 'USD' }, 12, 12);
  m.recordFxRate({ from: 'USD', to: 'CNY', rate: 7.1 }, 20, 20);
  m.revalueCollateral({ collateralId: 'COL-A', subsidiaryId: 'A', value: 6_500_000, currency: 'CNY' }, 28, 28);
  m.correctDrawdown({ subsidiaryId: 'A', amount: 7_000_000, currency: 'CNY' }, 30);
  return m;
}

test('验收：时间30追溯上调A借款为700万后，重现时间30总敞口触碰预警线并告警', () => {
  const m = buildAcceptanceScenario();
  const report = m.check(30, 30);
  // 700万 CNY + 40万 USD × 7.10 = 984万 ≥ 预警线 900万
  assert.equal(report.totalExposure, 9_840_000);
  assert.equal(report.warningLine, 9_000_000);
  assert.equal(report.breached, true);
  assert.equal(report.alerts.length, 1);
  assert.equal(report.alerts[0].kind, 'WARNING_BREACH');
  assert.ok(m.alertLog.some((a) => a.kind === 'WARNING_BREACH' && a.validTime === 30));
});

test('双时态：同一时间30、修正前知识(tx=29)回看则不触线', () => {
  const m = buildAcceptanceScenario();
  const before = m.check(30, 29);
  assert.equal(before.totalExposure, 8_840_000); // 600万 + 284万
  assert.equal(before.breached, false);
  const after = m.check(30, 30);
  assert.equal(after.totalExposure, 9_840_000); // 700万 + 284万
  assert.equal(after.breached, true);
});

test('汇率波动影响历史时点敞口折算', () => {
  const m = buildAcceptanceScenario();
  // t=15 时汇率仍为 7.00：600万 + 280万 = 880万
  assert.equal(m.check(15, 15).totalExposure, 8_800_000);
  // t=25 时汇率已为 7.10：600万 + 284万 = 884万
  assert.equal(m.check(25, 25).totalExposure, 8_840_000);
});

test('授信协议有效期外的子公司敞口不计入集团总敞口', () => {
  const m = new ExposureMonitor();
  m.setGroupLimit({ limit: 10_000_000, currency: 'CNY', warningRatio: 0.9 }, 0, 0);
  m.addAgreement({ subsidiaryId: 'A', limit: 8_000_000, currency: 'CNY' }, 0, 0, 40); // 协议 t=40 到期
  m.recordDrawdown({ subsidiaryId: 'A', amount: 6_000_000, currency: 'CNY' }, 10, 10);
  assert.equal(m.check(30, 30).totalExposure, 6_000_000); // 有效期内计入
  assert.equal(m.check(45, 45).totalExposure, 0); // 到期后不计入
});

test('担保物价值变动触发质押率重算并留痕', () => {
  const m = buildAcceptanceScenario();
  const reval = m.pledgeTrace.filter((e) => e.reason === 'collateral-revaluation');
  assert.equal(reval.length, 2); // t=10 初始登记 + t=28 贬值
  const last = reval[reval.length - 1];
  assert.equal(last.validTime, 28);
  assert.equal(last.collateralValue, 6_500_000);
  assert.equal(last.outstanding, 6_000_000); // t=28 时修正尚未记录
  assert.equal(last.pledgeRatio, 0.92); // 600万 / 650万
  assert.equal(last.breached, true); // 超过 80% 上限
  assert.ok(m.alertLog.some((a) => a.kind === 'PLEDGE_RATIO_BREACH' && a.validTime === 28));
});

test('借款追溯修正触发质押率重算，且可回溯任意双时态点', () => {
  const m = buildAcceptanceScenario();
  const corr = m.pledgeTrace.filter((e) => e.reason === 'drawdown-correction');
  assert.equal(corr.length, 1);
  assert.equal(corr[0].txTime, 30);
  assert.equal(corr[0].outstanding, 7_000_000);
  assert.equal(corr[0].pledgeRatio, 1.08); // 700万 / 650万
  assert.equal(corr[0].breached, true);
  // 双时态回溯：同一业务时间 t=28，修正前/后知识下的质押率不同
  const before = m.pledgeRatioAt('A', 28, 29)!;
  const after = m.pledgeRatioAt('A', 28, 30)!;
  assert.ok(Math.abs(before - 6_000_000 / 6_500_000) < 1e-9);
  assert.ok(Math.abs(after - 7_000_000 / 6_500_000) < 1e-9);
});

test('修正历史留痕：旧记录事务时间被关闭，新记录自原生效时间追溯生效', () => {
  const m = buildAcceptanceScenario();
  const history = m.drawdownHistory().filter((r) => r.payload.subsidiaryId === 'A');
  assert.equal(history.length, 2);
  const [oldRec, newRec] = history;
  assert.equal(oldRec.payload.amount, 6_000_000);
  assert.equal(oldRec.txTo, 30); // 旧记录在 tx=30 被关闭
  assert.equal(newRec.payload.amount, 7_000_000);
  assert.equal(newRec.validFrom, 10); // 追溯至原借款生效时间
  assert.equal(newRec.txFrom, 30);
});
