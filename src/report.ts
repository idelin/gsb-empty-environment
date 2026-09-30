import type { LayoutResult } from './dp.ts';

/** 每行度量与决策证据, 以及分开报告的三类异常: 负剩余宽 / 不可断长词 / (预算中断由调用方补充)。 */
export function formatLayout(result: LayoutResult, title: string, optimal: boolean): string {
  const out: string[] = [];
  out.push(`== ${title} ==`);
  out.push(`性质: ${optimal ? '全局最优 (DP, 可对照穷举)' : '启发式局部结果, 不保证最优'}`);
  for (const line of result.lines) {
    const m = line.measure;
    out.push(
      `行 ${line.index + 1} | "${m.text}"` +
        ` | 自然宽 ${m.natural} | 剩余 ${m.remaining}` +
        ` | 伸/缩 ${m.stretch}/${m.shrink}` +
        ` | 劣度 ${m.badness.toFixed(4)}` +
        ` | 断词罚 ${m.hyphenated ? m.hyphenPenalty : 0}` +
        ` | 邻行罚 ${line.adjPenalty.toFixed(4)}` +
        ` | 溢出 ${m.overflow}` +
        ` | 小计 ${line.cost.toFixed(4)} | 累计 ${line.cumulative.toFixed(4)}`,
    );
  }
  out.push(
    `总成本 ${result.cost.toFixed(4)} | 断词 ${result.hyphenCount} 次 | 探索状态 ${result.statesExplored}`,
  );
  const tight = result.lines.filter((l) => l.measure.remaining < 0 && l.measure.overflow === 0);
  const overflow = result.lines.filter((l) => l.measure.overflow > 0);
  out.push(
    `[报告] 负剩余宽行: ${
      tight.length === 0
        ? '无'
        : tight.map((l) => `行${l.index + 1}(剩余${l.measure.remaining}, 收缩吸收)`).join(', ')
    }`,
  );
  out.push(
    `[报告] 不可断长词溢出: ${
      overflow.length === 0
        ? '无'
        : overflow.map((l) => `行${l.index + 1}("${l.measure.text}" 溢出${l.measure.overflow})`).join(', ')
    }`,
  );
  return out.join('\n');
}
