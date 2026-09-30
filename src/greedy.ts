import { breakPositions, type Element } from './elements.ts';
import { measureLine } from './cost.ts';
import type { LayoutConfig } from './model.ts';
import { buildEvidence, type LayoutResult } from './dp.ts';

/**
 * 首次可行贪心: 每行尽量塞到最满 (取最后一个可行断点)。
 * 局部选择, 不保证全局最优 —— 结果只能标注为启发式, 不得称最优。
 * 不可断长词独占一行并溢出, 循环有上界保护, 不会死循环。
 */
export function layoutGreedy(elements: Element[], config: LayoutConfig): LayoutResult {
  const positions = breakPositions(elements);
  const end = elements.length;
  const breaks: number[] = [];
  let from = -1;
  let guard = 0;
  const guardLimit = elements.length + 1;
  while (from < end) {
    let chosen = -1;
    for (const q of positions) {
      if (q <= from) continue;
      const m = measureLine(elements, from, q, config);
      if (m.feasible) {
        chosen = q; // 继续尝试更满的断点
      } else {
        break; // 内容只增不减, 之后必然也不可行
      }
    }
    if (chosen === -1) {
      // 首个断点必可行 (单盒行允许溢出); 走到这里说明输入异常
      throw new Error('贪心排版找不到任何可行断点');
    }
    if (chosen === end) break;
    breaks.push(chosen);
    from = chosen;
    if (++guard > guardLimit) throw new Error('贪心排版未收敛 (疑似死循环)');
  }
  const evidence = buildEvidence(elements, config, breaks);
  const cost = evidence.lines.length === 0 ? 0 : evidence.lines[evidence.lines.length - 1].cumulative;
  return { ...evidence, cost, statesExplored: 0 };
}
