/**
 * 报告渲染：输入、中间精确值、分歧原点、差异链、反事实确认、搜索状态。
 */

import type { ComparisonResult, Divergence } from './compare.ts';
import type { Explanation } from './counterfactual.ts';
import type { SearchResult } from './search.ts';
import { Rat } from './rational.ts';
import type { Unit } from './units.ts';

const CAUSE_LABEL: Record<Divergence['cause'], string> = {
  'rounding-rule': '舍入规则分歧',
  'unit-conversion': '单位换算口径分歧',
  'denominator-convention': '分母口径分歧',
  'constant-value': '常量取值分歧',
  'unit-mismatch': '单位维度错误',
  structure: '运算结构分歧',
  unknown: '未分类分歧',
};

const STATUS_LABEL: Record<ComparisonResult['finalStatus'], string> = {
  equal: '最终输出精确一致',
  different: '最终输出不同',
  'unit-mismatch': '最终输出单位口径不同（硬性单位错误，不容差掩盖）',
  unlocatable: '输出不同但缺少中间证据，无法定位',
};

function fmt(v: Rat | null, u: Unit | null): string {
  if (v === null || u === null) return '<无记录>';
  const dec = v.toDecimalExact();
  const val = dec !== null && v.d !== 1n ? `${dec}（=${v.toString()}）` : v.display();
  return `${val} [${u.name}]`;
}

function fmtInputs(inputs: Record<string, Rat>): string {
  return Object.entries(inputs)
    .map(([k, v]) => `${k}=${v.display()}`)
    .join(', ');
}

export function renderComparison(cmp: ComparisonResult): string {
  const lines: string[] = [];
  lines.push(`== 比较 ${cmp.pipelineA} vs ${cmp.pipelineB} ==`);
  lines.push(`输入: ${fmtInputs(cmp.inputs)}`);
  lines.push(`结论: ${STATUS_LABEL[cmp.finalStatus]}`);
  lines.push(`  A 输出: ${fmt(cmp.finalA.value, cmp.finalA.unit)}  (${cmp.finalA.detail})`);
  lines.push(`  B 输出: ${fmt(cmp.finalB.value, cmp.finalB.unit)}  (${cmp.finalB.detail})`);

  if (cmp.origins.length > 0) {
    lines.push(`分歧原点（首个导致差异的规则子集，共 ${cmp.origins.length} 处）:`);
    for (const o of cmp.origins) {
      lines.push(`  [${CAUSE_LABEL[o.cause]}] ${cmp.pipelineA}:${o.aId} ~ ${cmp.pipelineB}:${o.bId}（${o.how === 'positional' ? '位置对齐' : '结构对齐'}）`);
      lines.push(`    A: ${fmt(o.aValue, o.aUnit)}  ← ${o.aDetail}`);
      lines.push(`    B: ${fmt(o.bValue, o.bUnit)}  ← ${o.bDetail}`);
    }
  }

  for (const [i, chain] of cmp.chains.entries()) {
    lines.push(`差异链 #${i + 1}（${chain.reachesOutput ? '延伸至输出' : '未延伸至输出，下游被后续运算吸收或证据缺失'}）:`);
    for (const s of chain.steps) {
      lines.push(`  ${s.aId} ~ ${s.bId}: A=${fmt(s.a.value, s.a.unit)}  B=${fmt(s.b.value, s.b.unit)}`);
    }
  }

  if (cmp.intermediateOnly.length > 0) {
    lines.push(`注意：最终输出一致，但存在 ${cmp.intermediateOnly.length} 处中间分歧（已记录，不可忽略）:`);
    for (const o of cmp.intermediateOnly) {
      lines.push(`  [${CAUSE_LABEL[o.cause]}] ${o.aId} ~ ${o.bId}: A=${fmt(o.aValue, o.aUnit)}  B=${fmt(o.bValue, o.bUnit)}`);
    }
  }

  if (cmp.missingEvidence.length > 0) {
    lines.push(`缺失中间证据的节点对: ${cmp.missingEvidence.join('; ')}`);
  }
  if (cmp.unmatchedA.length > 0 || cmp.unmatchedB.length > 0) {
    lines.push(`未能对齐的节点: A=[${cmp.unmatchedA.join(', ')}] B=[${cmp.unmatchedB.join(', ')}]`);
  }
  return lines.join('\n');
}

export function renderExplanations(exps: Explanation[]): string {
  if (exps.length === 0) return '反事实确认：无可用的规则替换候选';
  const lines = ['反事实确认（替换 A 的规则 → 独立重算 → 与 B 精确比对）:'];
  for (const e of exps) {
    const mark = e.confirmed ? '✓ 已确认' : '✗ 未确认';
    lines.push(`  ${mark}: ${e.descriptions.join('；')}`);
    if (!e.confirmed && e.residual) {
      lines.push(`    残差: A'=${e.residual.aPrime} vs B=${e.residual.b}`);
    }
  }
  return lines.join('\n');
}

export function renderSearch(res: SearchResult): string {
  switch (res.status) {
    case 'found': {
      const lines = [`最小反例：已找到（共求值 ${res.checked} 次）`, `输入: ${fmtInputs(res.inputs)}`];
      lines.push(renderComparison(res.comparison));
      return lines.join('\n');
    }
    case 'exhausted-space':
      return `最小反例：搜索空间已耗尽（求值 ${res.checked} 次），该空间内两条流水无分歧`;
    case 'budget-exhausted':
      return `最小反例：预算耗尽（已求值 ${res.checked} 次），状态未决——既不能确认等价，也不能给出反例`;
  }
}
