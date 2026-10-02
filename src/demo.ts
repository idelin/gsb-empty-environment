/**
 * 运行样例：多币种联合授信时态敞口监控。
 *
 * 场景时间线：
 *  t=0   集团授信协议生效：额度 1000 万 CNY，预警线 70%，协议有效期 [0, 50)
 *  t=10  子公司 A 借款 600 万 CNY，并质押担保物 800 万 CNY
 *  t=12  子公司 B 借款 5 万 USD（当时汇率 7.0）
 *  t=20  USD/CNY 汇率上调至 7.25
 *  t=25  A 的担保物重估下跌至 650 万
 *  t=30  追溯修正：A 自 t=10 起的历史借款由 600 万上调为 700 万
 *  t=55  协议已过期后的评估
 */
import { ExposureMonitor } from './monitor.ts';
import type { ExposureReport } from './monitor.ts';

const monitor = new ExposureMonitor({ baseCurrency: 'CNY' });

monitor.recordCreditLine(
  { groupId: 'G', limitAmount: 10_000_000, currency: 'CNY', warningRatio: 0.7 },
  0,
  50,
  0,
);
monitor.recordLoan({ subsidiary: 'A', amount: 6_000_000, currency: 'CNY' }, 10, Infinity, 10);
monitor.recordCollateral({ owner: 'A', value: 8_000_000, currency: 'CNY' }, 10, 10);
monitor.recordLoan({ subsidiary: 'B', amount: 50_000, currency: 'USD' }, 12, Infinity, 12);
monitor.recordFxRate({ from: 'USD', to: 'CNY', rate: 7.0 }, 0, 0);

function fmt(value: number | null): string {
  return value === null ? 'N/A' : value.toLocaleString('zh-CN', { maximumFractionDigits: 2 });
}

function printReport(title: string, report: ExposureReport): void {
  console.log(`\n=== ${title} (validTime=${report.validTime}, txTime=${report.txTime}) ===`);
  console.log(`授信额度: ${fmt(report.limit)} ${report.baseCurrency} | 预警线: ${fmt(report.warningLine)}`);
  for (const item of report.items) {
    console.log(
      `  敞口 ${item.subsidiary}: ${fmt(item.amount)} ${item.currency}` +
        ` (汇率 ${item.fxRate ?? 'N/A'}) -> ${fmt(item.converted)} ${report.baseCurrency}`,
    );
  }
  console.log(
    `总敞口: ${fmt(report.totalExposure)} ${report.baseCurrency}` +
      ` | 利用率: ${report.utilization === null ? 'N/A' : (report.utilization * 100).toFixed(2) + '%'}` +
      ` | 触碰预警线: ${report.breached ? '是' : '否'}`,
  );
  for (const pledge of report.pledges) {
    console.log(
      `  质押 ${pledge.owner}: 借款 ${fmt(pledge.loanAmount)} / 担保物 ${fmt(pledge.collateralValue)}` +
        ` = 质押率 ${(pledge.pledgeRatio * 100).toFixed(2)}%`,
    );
  }
  for (const alert of report.alerts) {
    console.log(`  [告警 ${alert.code}] ${alert.message}`);
  }
  if (report.alerts.length === 0) console.log('  (无告警)');
}

printReport('t=12 初始状态', monitor.evaluate(12, 12));

monitor.recordFxRate({ from: 'USD', to: 'CNY', rate: 7.25 }, 20, 20);
printReport('t=20 汇率上调至 7.25', monitor.evaluate(20, 20));

monitor.recordCollateral({ owner: 'A', value: 6_500_000, currency: 'CNY' }, 25, 25);
printReport('t=26 担保物重估下跌至 650 万', monitor.evaluate(26, 26));

console.log('\n--- t=30 追溯修正：A 自 t=10 起的历史借款 600 万 -> 700 万 ---');
monitor.recordLoan({ subsidiary: 'A', amount: 7_000_000, currency: 'CNY' }, 10, Infinity, 30);

printReport('重现 t=30（修正后系统认知）', monitor.evaluate(30, 30));
printReport('对照：t=30 时点但 txTime=29（修正前系统认知）', monitor.evaluate(30, 29));

printReport('t=55 协议已过期', monitor.evaluate(55, 55));

console.log('\n=== 质押率重算追溯日志 ===');
for (const entry of monitor.getPledgeTrace()) {
  console.log(
    `  [validTime=${entry.validTime}, txTime=${entry.txTime}] ${entry.owner} 质押率 ` +
      `${entry.previousRatio === null ? 'N/A' : (entry.previousRatio * 100).toFixed(2) + '%'} -> ` +
      `${(entry.pledgeRatio * 100).toFixed(2)}%（原因: ${entry.causes.join('、')}）`,
  );
}
