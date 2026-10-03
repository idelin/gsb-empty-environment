/**
 * 验收场景：Q1 追溯调整。
 *
 * - Q1 考核周期为第 [1, 91) 天（90 天）。
 * - v1（第 1 天，张伟）：目标A 权重 0.6 在 [1, 60) 有效，[60, 91) 为 0.5；目标B 对应 0.4 / 0.5。
 * - v2（第 45 天，李娜）：追溯调整为全季度目标A 权重 0.4、目标B 权重 0.6。
 * - 输出调整前后实际得分差，并保留各版本责任人签名。
 */
import { TemporalKpiLedger } from '../src/index.ts';
import type { ScoreReport } from '../src/index.ts';

const ledger = new TemporalKpiLedger({
  periodFrom: 1,
  periodTo: 91,
  keys: {
    '张伟': 'secret-key-zhangwei',
    '李娜': 'secret-key-lina',
  },
});

// 季度末实际完成值
ledger.recordActual('目标A', 108); // 达成率 108/100 = 1.08
ledger.recordActual('目标B', 72);  // 达成率 72/80 = 0.90

// v1：期初战略
ledger.commitVersion({
  author: '张伟',
  recordedAtDay: 1,
  reason: 'Q1 期初战略：目标A 为主攻指标',
  entries: [
    { kpiId: '目标A', target: 100, weight: 0.6, validFrom: 1, validTo: 60 },
    { kpiId: '目标A', target: 100, weight: 0.5, validFrom: 60, validTo: 91 },
    { kpiId: '目标B', target: 80, weight: 0.4, validFrom: 1, validTo: 60 },
    { kpiId: '目标B', target: 80, weight: 0.5, validFrom: 60, validTo: 91 },
  ],
});

// v2：第 45 天追溯调整，覆盖全季度
ledger.commitVersion({
  author: '李娜',
  recordedAtDay: 45,
  reason: '组织战略调整：目标B 升为季度主攻指标，追溯至全季度生效',
  entries: [
    { kpiId: '目标A', target: 100, weight: 0.4, validFrom: 1, validTo: 91 },
    { kpiId: '目标B', target: 80, weight: 0.6, validFrom: 1, validTo: 91 },
  ],
});

function printReport(title: string, report: ScoreReport): void {
  console.log(`\n== ${title}（版本 v${report.versionId}）==`);
  for (const segment of report.segments) {
    const parts = segment.contributions
      .map((c) => `${c.kpiId}: 权重${c.weight} × 达成率${c.rate.toFixed(2)} = ${c.weighted.toFixed(4)}`)
      .join('，');
    console.log(`  第 [${segment.from}, ${segment.to}) 天（${segment.days} 天）段得分 ${segment.score.toFixed(4)} ｜ ${parts}`);
  }
  console.log(`  季度末最终加权得分: ${report.totalScore.toFixed(4)}`);
}

const { base, target, delta } = ledger.compareVersions(1, 2);
printReport('调整前（历史战略快照推演）', base);
printReport('调整后（最新版本）', target);
console.log(`\n调整前后实际得分差: ${delta >= 0 ? '+' : ''}${delta.toFixed(4)}`);

console.log('\n== 版本影响记录 ==');
for (const impact of ledger.impacts()) {
  const prev = impact.previousScore === null ? '—' : impact.previousScore.toFixed(4);
  const next = impact.newScore === null ? '—' : impact.newScore.toFixed(4);
  const diff = impact.delta === null ? '—' : `${impact.delta >= 0 ? '+' : ''}${impact.delta.toFixed(4)}`;
  console.log(`  v${impact.versionId}（第 ${impact.recordedAtDay} 天，${impact.author}）：${prev} -> ${next}（Δ ${diff}）｜${impact.reason}`);
}

console.log('\n== 责任人版本签名 ==');
for (const version of ledger.listVersions()) {
  const valid = ledger.verifyVersion(version.id);
  console.log(`  v${version.id} ${version.author} 签名 ${version.signature.slice(0, 16)}… 校验${valid ? '通过' : '失败'}`);
}
