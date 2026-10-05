import type { ExplanationNode, RankingResult } from './types.ts';

const KIND_LABEL: Record<ExplanationNode['kind'], string> = {
  points: '总积分',
  'head-to-head': '相互战绩',
  resolved: '确定名次',
  tied: '并列',
};

/** 把解释树渲染成缩进文本，便于日志与调试 */
export function formatExplanation(node: ExplanationNode, indent = ''): string {
  const lines = [`${indent}- [${KIND_LABEL[node.kind]}] ${node.summary}`];
  for (const child of node.children) {
    lines.push(formatExplanation(child, `${indent}  `));
  }
  return lines.join('\n');
}

/** 渲染最终排名表 */
export function formatStandings(result: RankingResult): string {
  return result.standings
    .map((s) => `第${s.rank}名 ${s.teams.join('、')}（总积分 ${s.points}）`)
    .join('\n');
}
