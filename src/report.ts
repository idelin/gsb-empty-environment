import type { MigrationReport } from './migrate.ts';

function formatRange(range: { file: string; start: { line: number; column: number }; end: { line: number; column: number } }): string {
  return `${range.file}:${range.start.line}:${range.start.column}-${range.end.line}:${range.end.column}`;
}

/** 生成人类可读的文本报告。 */
export function formatTextReport(report: MigrationReport): string {
  const lines: string[] = [];
  const s = report.summary;
  lines.push('覆盖率迁移报告');
  lines.push(`  旧语句 ${s.oldTotal} 条，新语句 ${s.newTotal} 条`);
  lines.push(`  继承 ${s.inherited} / 新增 ${s.added} / 不确定 ${s.uncertain} / 被拒绝规则 ${s.rejectedRules}`);
  lines.push('');

  lines.push(`== 继承（${report.inherited.length}）==`);
  for (const entry of report.inherited) {
    const sources = entry.sources.map((p) => `${p.oldId}(hits=${p.hits})`).join(' + ');
    lines.push(`  ${entry.newId} <= ${sources} [${entry.via}] hits=${entry.hits}`);
    if (entry.note) lines.push(`    备注: ${entry.note}`);
  }
  lines.push('');

  lines.push(`== 新增（${report.added.length}）==`);
  for (const entry of report.added) {
    lines.push(`  ${entry.newId} @ ${formatRange(entry.range)}`);
  }
  lines.push('');

  lines.push(`== 不确定（${report.uncertain.length}）==`);
  for (const entry of report.uncertain) {
    if (entry.type === 'ambiguous-fingerprint') {
      lines.push(`  指纹冲突 ${entry.fingerprint}: 旧[${entry.oldIds.join(', ')}] <-> 新[${entry.newIds.join(', ')}]`);
      lines.push(`    ${entry.detail}`);
    } else if (entry.type === 'rejected-rule') {
      lines.push(`  规则被拒绝: ${entry.rule.kind} ${entry.affectedOldIds.join(', ')} -> ${entry.affectedNewIds.join(', ')}`);
      lines.push(`    原因: ${entry.reason}`);
    } else {
      lines.push(`  旧语句无对应: ${entry.oldId} @ ${formatRange(entry.range)}`);
      lines.push(`    ${entry.detail}`);
    }
  }
  return lines.join('\n');
}
