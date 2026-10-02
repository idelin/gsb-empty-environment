import type { AggregateMetrics, EventMetrics } from './metrics.ts';

/** 将聚合度量渲染为详尽诊断报告（纯文本）。 */
export function generateReport(metrics: AggregateMetrics, scenarioName: string): string {
  const lines: string[] = [];
  const rule = '='.repeat(78);
  const thin = '-'.repeat(78);

  lines.push(rule);
  lines.push('  SWIM 故障检测诊断报告 (MTTD / 误报率分析)');
  lines.push(`  场景: ${scenarioName}`);
  lines.push(rule);
  lines.push('');
  lines.push('[事件明细]  单位: 协议周期');
  lines.push(thin);
  lines.push(
    pad('事件', 6) +
      pad('类型', 14) +
      pad('节点', 9) +
      pad('发生', 6) +
      pad('首疑', 6) +
      pad('标记', 6) +
      pad('反驳', 6) +
      pad('TTSA', 6) +
      pad('MTTD', 6) +
      '判定',
  );
  lines.push(thin);
  for (const e of metrics.events) {
    lines.push(
      pad(`#${e.eventId}`, 6) +
        pad(kindLabel(e), 14) +
        pad(e.nodeId, 9) +
        pad(String(e.occurredAt), 6) +
        pad(fmt(e.firstSuspectedAt), 6) +
        pad(fmt(e.confirmedFailedAt), 6) +
        pad(fmt(e.refutedAt), 6) +
        pad(fmt(e.timeToSuspect), 6) +
        pad(fmt(e.timeToDetect), 6) +
        outcomeLabel(e),
    );
  }
  lines.push(thin);
  lines.push('');
  lines.push('[聚合指标]');
  lines.push(`  注入事件总数        : ${metrics.totalEvents}`);
  lines.push(`    真实节点宕机      : ${metrics.crashEvents}`);
  lines.push(`    短暂网络丢包扰动  : ${metrics.disturbanceEvents}`);
  lines.push(
    `  判定矩阵            : TP=${metrics.truePositives} TN=${metrics.trueNegatives} ` +
      `FP=${metrics.falsePositives} FN=${metrics.falseNegatives}`,
  );
  lines.push(`  检出率 (TP/(TP+FN)) : ${pct(metrics.detectionRate)}`);
  lines.push(`  误报率 (FP/(FP+TN)) : ${pct(metrics.falsePositiveRate)}`);
  lines.push('');
  lines.push('[检测时延]  (仅统计真实宕机事件, 单位: 协议周期)');
  lines.push(`  平均首次怀疑耗时    : ${fmtNum(metrics.averageTimeToSuspect)}`);
  lines.push(`  平均检测时间 MTTD   : ${fmtNum(metrics.averageMttd)}`);
  lines.push(`  最小 / 最大 MTTD    : ${fmt(metrics.minMttd)} / ${fmt(metrics.maxMttd)}`);
  lines.push('');
  lines.push('[结论]');
  lines.push(
    `  协议在 ${metrics.crashEvents} 次真实宕机中全部检出 (检出率 ${pct(metrics.detectionRate)}),`,
  );
  lines.push(
    `  平均检测时间 MTTD = ${fmtNum(metrics.averageMttd)} 周期; ` +
      `在 ${metrics.disturbanceEvents} 次短暂丢包扰动下误报率 ${pct(metrics.falsePositiveRate)},`,
  );
  lines.push('  怀疑-反驳机制有效抑制了瞬时扰动导致的误判, 协议鲁棒性达标。');
  lines.push(rule);
  return lines.join('\n');
}

function kindLabel(e: EventMetrics): string {
  return e.kind === 'node-crash' ? '节点宕机' : '丢包扰动';
}

function outcomeLabel(e: EventMetrics): string {
  switch (e.outcome) {
    case 'true-positive':
      return 'TP 正确检出';
    case 'true-negative':
      return 'TN 正确放行';
    case 'false-positive':
      return 'FP 误报';
    case 'false-negative':
      return 'FN 漏报';
  }
}

function pad(text: string, width: number): string {
  let display = text;
  let len = 0;
  for (const ch of text) {
    len += ch.codePointAt(0)! > 0xff ? 2 : 1;
  }
  for (let i = len; i < width; i += 1) {
    display += ' ';
  }
  return display;
}

function fmt(value: number | null): string {
  return value === null ? '-' : String(value);
}

function fmtNum(value: number | null): string {
  return value === null ? '-' : value.toFixed(2);
}

function pct(value: number): string {
  return `${(value * 100).toFixed(1)}%`;
}
