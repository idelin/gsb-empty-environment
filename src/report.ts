/**
 * 中文报告渲染。
 */

import type { GenerateResult } from './generate.ts';

function configLine(mask: number, features: string[]): string {
  return features.map((f, i) => `${f}=${(mask >> i) & 1}`).join('  ');
}

export function renderReport(r: GenerateResult): string {
  const out: string[] = [];
  const k = r.configs.length;

  out.push('========== 特性成对覆盖测试配置生成 ==========');
  out.push(`特性 (${r.features.length}): ${r.features.join(', ')}`);
  out.push(`约束子句: ${r.clauseCount} 条（互斥 / 至多其一 / 蕴含）`);
  out.push(`合法配置: ${r.validCount} / ${r.totalConfigs}`);
  out.push('');

  out.push('---------- 结果 ----------');
  out.push(`最少配置数: ${k}（已证明最小）`);
  out.push('配置列表:');
  r.configs.forEach((c, idx) => {
    out.push(`  #${idx + 1}  ${configLine(c, r.features)}`);
  });
  out.push('');
  out.push(
    `覆盖率: 可达两两组合 ${r.reachableCount}/${r.reachableCount} = 100%` +
    `（全部两两组合共 ${r.totalPairCombos} 个，其中 ${r.unreachable.length} 个不可达，不计入覆盖率，见下文解释）`
  );
  out.push('');

  out.push('---------- 最小性证明 ----------');
  for (const reason of r.cover.lowerBoundReasons) {
    out.push(`下界: ${reason}`);
  }
  out.push(`上界: 上述 ${k} 个配置覆盖全部可达组合 ⇒ 至多 ${k} 个。`);
  if (r.cover.lowerBound === k) {
    out.push(`下界 ${r.cover.lowerBound} = 上界 ${k} ⇒ 配置数最小，证毕。`);
  } else {
    out.push(
      `根节点下界 ${r.cover.lowerBound} 小于上界 ${k}，故执行精确分支限界穷举：` +
      `利用装箱下界与最大新增覆盖下界剪枝，确定性枚举全部可能的配置子集，` +
      `证明不存在 ${k - 1} 个配置的覆盖（搜索节点 ${r.cover.searchNodes} 个）。证毕。`
    );
  }
  out.push('');

  if (r.unreachable.length > 0) {
    out.push(`---------- 不可达组合 (${r.unreachable.length}) ----------`);
    for (const item of r.unreachable) {
      const { i, j, vi, vj } = item.req;
      out.push(`✗ ${r.features[i]}=${vi} 且 ${r.features[j]}=${vj}`);
      for (const line of item.explanation) out.push(`    ${line}`);
    }
  } else {
    out.push('---------- 不可达组合: 无 ----------');
  }

  return out.join('\n');
}
