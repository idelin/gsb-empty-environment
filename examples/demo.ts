/**
 * 运行样例：多币种联合授信时态敞口监控。
 *
 * 验收场景时间线：
 *   t=0  集团总授信 1000万 CNY，预警线 90%；子公司 A/B 协议生效；USD/CNY=7.00
 *   t=10 子公司 A 借款 600万 CNY；A 提供担保物 800万 CNY
 *   t=12 子公司 B 借款 40万 USD
 *   t=20 汇率波动：USD/CNY 上调为 7.10
 *   t=25 例行监控：总敞口 884万，未触线
 *   t=28 担保物贬值为 650万 → 触发质押率重算
 *   t=30 追溯上调 A 的历史借款为 700万 → 重现 t=30：总敞口 984万，触碰预警线并告警
 */
import { ExposureMonitor, type ExposureReport } from '../src/monitor.ts';

const fmt = (n: number): string => n.toLocaleString('zh-CN', { maximumFractionDigits: 2 });

function printReport(title: string, r: ExposureReport): void {
  console.log(`\n== ${title} (vt=${r.validTime}, tx=${r.txTime}) ==`);
  for (const b of r.breakdown) {
    console.log(`  子公司${b.subsidiaryId}: ${fmt(b.amount)} ${b.currency} × ${b.fxRate} = ${fmt(b.converted)} ${r.groupCurrency}`);
  }
  console.log(`  集团总敞口: ${fmt(r.totalExposure)} / 上限 ${fmt(r.groupLimit)} ${r.groupCurrency}（利用率 ${(r.utilization * 100).toFixed(1)}%）`);
  console.log(`  预警线: ${fmt(r.warningLine)} → ${r.breached ? '⚠ 已触碰' : '未触碰'}`);
  for (const a of r.alerts) console.log(`  🚨 [${a.kind}] ${a.message}`);
}

const monitor = new ExposureMonitor(0.8);

// t=0：集团授信 1000万 CNY，预警线 90%
monitor.setGroupLimit({ limit: 10_000_000, currency: 'CNY', warningRatio: 0.9 }, 0, 0);
monitor.addAgreement({ subsidiaryId: 'A', limit: 8_000_000, currency: 'CNY' }, 0, 0);
monitor.addAgreement({ subsidiaryId: 'B', limit: 5_000_000, currency: 'CNY' }, 0, 0);
monitor.recordFxRate({ from: 'USD', to: 'CNY', rate: 7.0 }, 0, 0);

// t=10：A 借款 600万 CNY，并提供 800万 担保物
monitor.recordDrawdown({ subsidiaryId: 'A', amount: 6_000_000, currency: 'CNY' }, 10, 10);
monitor.revalueCollateral({ collateralId: 'COL-A', subsidiaryId: 'A', value: 8_000_000, currency: 'CNY' }, 10, 10);

// t=12：B 借款 40万 USD
monitor.recordDrawdown({ subsidiaryId: 'B', amount: 400_000, currency: 'USD' }, 12, 12);

// t=20：汇率波动
monitor.recordFxRate({ from: 'USD', to: 'CNY', rate: 7.1 }, 20, 20);

// t=25：例行监控（600万 + 40万×7.10 = 884万 < 900万）
printReport('例行监控', monitor.check(25, 25));

// t=28：担保物贬值 800万 → 650万，触发质押率重算
monitor.revalueCollateral({ collateralId: 'COL-A', subsidiaryId: 'A', value: 6_500_000, currency: 'CNY' }, 28, 28);

// t=30：追溯上调 A 的历史借款 600万 → 700万（自 t=10 起生效）
monitor.correctDrawdown({ subsidiaryId: 'A', amount: 7_000_000, currency: 'CNY' }, 30);

// 验收：以 t=30 的全部知识重现时间 30 → 700万 + 284万 = 984万 ≥ 900万，触碰预警线
printReport('验收：重现时间30（含追溯修正）', monitor.check(30, 30));

// 双时态对照：同样看时间30，但只用 t=29 时系统已知的知识 → 修正尚未记录，不触线
printReport('对照：重现时间30（修正前知识 tx=29）', monitor.check(30, 29));

console.log('\n== 质押率重算留痕（pledgeTrace） ==');
for (const e of monitor.pledgeTrace) {
  const ratio = e.pledgeRatio === null ? 'N/A' : `${(e.pledgeRatio * 100).toFixed(1)}%`;
  console.log(
    `  tx=${e.txTime} vt=${e.validTime} [${e.reason}] 子公司${e.subsidiaryId}: ` +
      `敞口 ${fmt(e.outstanding)} / 担保物 ${fmt(e.collateralValue)} = ${ratio} ${e.breached ? '⚠ 超限' : ''}`,
  );
}

console.log('\n== 借款双时态历史（含被修正记录） ==');
for (const r of monitor.drawdownHistory()) {
  const txTo = r.txTo === Number.MAX_SAFE_INTEGER ? '∞' : String(r.txTo);
  console.log(`  #${r.id} 子公司${r.payload.subsidiaryId} ${fmt(r.payload.amount)} ${r.payload.currency}  valid=[${r.validFrom},∞) tx=[${r.txFrom},${txTo})`);
}

console.log('\n== 告警流水 ==');
for (const a of monitor.alertLog) console.log(`  [${a.kind}] vt=${a.validTime} tx=${a.txTime} ${a.message}`);
