import { toElements, type Element } from './elements.ts';
import { bestResult, layoutOptimal, solveTable, type Entry, type LayoutResult } from './dp.ts';
import type { LayoutConfig, Paragraph, ParagraphNode } from './model.ts';

export interface IncrementalResult {
  result: LayoutResult;
  /** 逐步证书: 编辑定位 -> 前沿重用 -> 继续搜索 -> 最优成本 -> 全量校验 */
  certificate: string[];
  reusedStates: number;
  recomputedStates: number;
  /** 与全量重算的最优成本是否一致 */
  verified: boolean;
}

function commonPrefixLength(a: Element[], b: Element[]): number {
  let i = 0;
  while (i < a.length && i < b.length && JSON.stringify(a[i]) === JSON.stringify(b[i])) i++;
  return i;
}

/**
 * 增量编辑: 替换第 nodeIndex 个节点后重排。
 * 断点 b 之前的行度量只依赖元素 0..b, 因此旧 DP 表中 b 落在未变前缀内的
 * 状态 (前沿) 成本不变, 直接作为新搜索的种子复用; 只重算受影响的后缀。
 */
export function layoutWithEdit(
  paragraph: Paragraph,
  nodeIndex: number,
  replacement: ParagraphNode,
  config: LayoutConfig,
  hyphenWidth = 5,
): IncrementalResult {
  const certificate: string[] = [];
  const oldElements = toElements(paragraph, hyphenWidth);
  const oldTable = solveTable(oldElements, config);

  const nodes = [...paragraph.nodes];
  if (nodeIndex < 0 || nodeIndex >= nodes.length) throw new Error(`节点下标越界: ${nodeIndex}`);
  const oldNode = nodes[nodeIndex];
  nodes[nodeIndex] = replacement;
  const newElements = toElements({ nodes }, hyphenWidth);

  const prefix = commonPrefixLength(oldElements, newElements);
  certificate.push(
    `[1] 编辑定位: 节点 #${nodeIndex} (${describe(oldNode)} -> ${describe(replacement)}), 元素公共前缀长度 ${prefix}`,
  );

  const seed = new Map<string, Entry>();
  for (const [k, e] of oldTable.entries) {
    if (e.b < prefix) seed.set(k, e); // 断点 b 未越前缀, 状态成本不变
  }
  certificate.push(`[2] 重用 DP 前沿状态 ${seed.size} 个 (断点 b < ${prefix}, 行度量不受编辑影响)`);

  const table = solveTable(newElements, config, seed);
  const result = bestResult(newElements, config, table);
  const recomputed = table.entries.size - seed.size;
  certificate.push(`[3] 自前沿继续搜索: 新评估状态 ${recomputed} 个 (受影响后缀)`);
  certificate.push(`[4] 最优成本 ${result.cost.toFixed(4)}, 断点 [${result.breaks.join(', ')}]`);

  const full = layoutOptimal(newElements, config);
  const verified = Math.abs(result.cost - full.cost) <= 1e-6;
  certificate.push(
    `[5] 全量重算交叉校验: 成本 ${full.cost.toFixed(4)} — ${verified ? '一致 ✓' : '不一致 ✗'}`,
  );

  return {
    result,
    certificate,
    reusedStates: seed.size,
    recomputedStates: recomputed,
    verified,
  };
}

function describe(node: ParagraphNode): string {
  return node.kind === 'word' ? `词 "${node.text}"` : '空格';
}
