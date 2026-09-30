import { breakPositions, isHyphenBreak, type Element } from './elements.ts';
import { lineCost, measureLine } from './cost.ts';
import type { LayoutConfig } from './model.ts';
import { EPS } from './dp.ts';

export interface ExhaustiveResult {
  status: 'ok' | 'budget-exceeded';
  /** 仅 status === 'ok' 时是最优成本; 预算中断时为 null, 不得称最优 */
  cost: number | null;
  breaks: number[] | null;
  nodesVisited: number;
  budget: number;
}

function earlierPath(a: number[], b: number[]): boolean {
  for (let i = 0; i < Math.min(a.length, b.length); i++) {
    if (a[i] !== b[i]) return a[i] < b[i];
  }
  return a.length < b.length;
}

/**
 * 小段落穷举全部断点序列, 与 DP 使用完全相同的可加代价模型, 用于对照验证。
 * 访问节点数超过 budget 即中断并单独报告, 此时不给出任何最优性结论。
 */
export function layoutExhaustive(
  elements: Element[],
  config: LayoutConfig,
  budget = 200000,
): ExhaustiveResult {
  const positions = breakPositions(elements);
  const end = elements.length;
  let best = Infinity;
  let bestPath: number[] | null = null;
  let visited = 0;
  let exceeded = false;
  const path: number[] = [];

  function dfs(
    from: number,
    hyphens: number,
    prevRemaining: number | null,
    prevHyphenated: boolean,
    costSoFar: number,
  ): void {
    if (exceeded) return;
    if (++visited > budget) {
      exceeded = true;
      return;
    }
    if (costSoFar > best + EPS) return; // 代价非负, 可剪枝
    for (const q of positions) {
      if (q <= from) continue;
      const m = measureLine(elements, from, q, config);
      if (!m.feasible) break; // 内容单调增加, 之后也不可行
      const flagQ = isHyphenBreak(elements, q);
      if (prevHyphenated && flagQ) continue; // 禁止连续断词
      const h2 = hyphens + (flagQ ? 1 : 0);
      if (h2 > config.hyphenBudget) continue; // 断词预算
      const adj = prevRemaining === null ? 0 : config.adjacencyWeight * Math.abs(prevRemaining - m.remaining);
      const cost = costSoFar + lineCost(m, config) + adj;
      if (q === end) {
        if (cost < best - EPS || (Math.abs(cost - best) <= EPS && (bestPath === null || earlierPath(path, bestPath)))) {
          best = cost;
          bestPath = [...path];
        }
      } else {
        path.push(q);
        dfs(q, h2, m.remaining, flagQ, cost);
        path.pop();
      }
    }
  }

  dfs(-1, 0, null, false, 0);
  if (exceeded) {
    return { status: 'budget-exceeded', cost: null, breaks: null, nodesVisited: visited, budget };
  }
  return { status: 'ok', cost: best, breaks: bestPath, nodesVisited: visited, budget };
}
