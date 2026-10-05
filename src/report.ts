import type { FieldDef, Scalar } from './model.ts';
import type { AnalysisReport } from './analyze.ts';

// Compress consecutive integers into [lo, hi] runs.
export function toRuns(values: Scalar[]): [number, number][] {
  const nums = values as number[];
  const runs: [number, number][] = [];
  let start = 0;
  for (let i = 1; i <= nums.length; i++) {
    if (i === nums.length || nums[i] !== nums[i - 1] + 1) {
      runs.push([nums[start], nums[i - 1]]);
      start = i;
    }
  }
  return runs;
}

// JSON-friendly report: int-field value sets become { runs: [[lo, hi], ...] },
// enum-field sets stay plain arrays.
export function toJsonReport(report: AnalysisReport, fields: FieldDef[]): unknown {
  const fieldByName = new Map(fields.map((f) => [f.name, f]));
  const encode = (fname: string, values: Scalar[]): unknown =>
    fieldByName.get(fname)!.kind === 'int' ? { runs: toRuns(values) } : values;
  const encodePattern = (pattern: Record<string, Scalar[] | 'any'>) =>
    Object.fromEntries(
      Object.entries(pattern).map(([k, v]) => [k, v === 'any' ? 'any' : encode(k, v)]),
    );
  const encodeRegion = (region: Record<string, Scalar[]>) =>
    Object.fromEntries(Object.entries(region).map(([k, v]) => [k, encode(k, v)]));

  return {
    ...report,
    rules: report.rules.map((r) => ({
      ...r,
      witness: r.witness && { ...r.witness, pattern: encodePattern(r.witness.pattern) },
      coveredBy: r.coveredBy?.map((c) => ({ ...c, region: encodeRegion(c.region) })),
    })),
  };
}

// Compress consecutive integers into "lo..hi" runs; join enums with commas.
export function formatValues(field: FieldDef, values: Scalar[]): string {
  if (field.kind !== 'int') return `{${values.join(', ')}}`;
  const nums = values as number[];
  const runs: string[] = [];
  let start = 0;
  for (let i = 1; i <= nums.length; i++) {
    if (i === nums.length || nums[i] !== nums[i - 1] + 1) {
      const lo = nums[start];
      const hi = nums[i - 1];
      runs.push(lo === hi ? String(lo) : `${lo}..${hi}`);
      start = i;
    }
  }
  return runs.join(', ');
}

function formatInput(input: Record<string, Scalar>): string {
  const parts = Object.entries(input).map(([k, v]) => `${k}: ${JSON.stringify(v)}`);
  return `{ ${parts.join(', ')} }`;
}

export function formatReport(report: AnalysisReport, fields: FieldDef[]): string {
  const fieldByName = new Map(fields.map((f) => [f.name, f]));
  const lines: string[] = [];
  const title = report.tableName ? `决策表 "${report.tableName}"` : '决策表';
  lines.push(`${title}: ${report.ruleCount} 条规则, ${report.fieldCount} 个字段`);
  lines.push('='.repeat(60));

  for (const r of report.rules) {
    const action = r.action ? `  action=${r.action}` : '';
    lines.push('');
    lines.push(`规则 ${r.ruleId}  (priority=${r.priority}${action})`);
    if (r.status === 'unsatisfiable') {
      lines.push('  状态: 不可满足 —— 条件本身矛盾, 任何输入都无法命中');
      continue;
    }
    if (r.status === 'winnable' && r.witness) {
      const w = r.witness;
      lines.push('  状态: 可胜出');
      lines.push(
        `  最小见证 (约束 ${w.constrainedFields}/${report.fieldCount} 个字段, ` +
        `保证 ${w.guaranteedInputs} 个输入全部胜出):`,
      );
      for (const [fname, spec] of Object.entries(w.pattern)) {
        const rendered = spec === 'any'
          ? '任意'
          : formatValues(fieldByName.get(fname)!, spec);
        lines.push(`    ${fname} = ${rendered}`);
      }
      lines.push(`  示例输入: ${formatInput(w.input)}`);
      continue;
    }
    lines.push('  状态: 被覆盖 —— 任何命中本规则的输入都会被其他规则截获');
    for (const c of r.coveredBy ?? []) {
      const rel = c.relation === 'higher-priority' ? '更高优先级' : '同优先级 (冲突)';
      const region = Object.entries(c.region)
        .map(([fname, vals]) => `${fname} ∈ ${formatValues(fieldByName.get(fname)!, vals)}`)
        .join(', ');
      lines.push(`    - ${c.ruleId} (${rel}) 覆盖区域: ${region || '(全部域)'}`);
    }
  }

  lines.push('');
  lines.push('-'.repeat(60));
  if (report.conflicts.length === 0) {
    lines.push('冲突: 无 (同优先级规则互不重叠)');
  } else {
    lines.push(`冲突: ${report.conflicts.length} 组同优先级规则可同时命中`);
    for (const c of report.conflicts) {
      const tag = c.effective ? '有效冲突' : '被更高优先级遮蔽';
      lines.push(`  ${c.ruleIds[0]} × ${c.ruleIds[1]} (priority=${c.priority}, ${tag})`);
      lines.push(`    同时命中见证: ${formatInput(c.witnessInput)}`);
    }
  }
  return lines.join('\n');
}
