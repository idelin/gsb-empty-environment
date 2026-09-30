import { breakPositions, isHyphenBreak, type Element } from './elements.ts';
import { lineCost, measureLine, type LineMeasure } from './cost.ts';
import type { LayoutConfig } from './model.ts';

export const EPS = 1e-9;

/**
 * DP 状态 (a, b, h): 最近两个断点为 a、b, 到 b 为止已用 h 次断词。
 * 历史约束全部编码在状态里:
 *  - a 提供上一行剩余宽 (相邻行宽度差罚分);
 *  - b 的元素类型提供 "上一行是否断词结尾" (禁止连续断词);
 *  - h 提供跨行一致的断词次数预算。
 */
export interface Entry {
  a: number;
  b: number;
  h: number;
  cost: number;
  predKey: string | null;
}

export interface Table {
  entries: Map<string, Entry>;
}

function key(a: number, b: number, h: number): string {
  return `${a}|${b}|${h}`;
}

/** 相同代价按较早断点决胜: 候选的前一断点更小者胜。 */
function better(candidateCost: number, candidatePrev: number, existing: Entry | undefined): boolean {
  if (existing === undefined) return true;
  if (candidateCost < existing.cost - EPS) return true;
  if (candidateCost > existing.cost + EPS) return false;
  return candidatePrev < existing.a;
}

export function solveTable(
  elements: Element[],
  config: LayoutConfig,
  seed?: Map<string, Entry>,
): Table {
  const entries = new Map<string, Entry>(seed);
  const positions = breakPositions(elements);
  const cache = new Map<string, LineMeasure>();
  const measure = (from: number, to: number): LineMeasure => {
    const cacheKey = `${from}|${to}`;
    let m = cache.get(cacheKey);
    if (!m) {
      m = measureLine(elements, from, to, config);
      cache.set(cacheKey, m);
    }
    return m;
  };

  // 起始状态: 段首 -> 第一个断点 (无上一行, 不计邻行罚)
  for (const b of positions) {
    const m = measure(-1, b);
    if (!m.feasible) continue;
    const h = isHyphenBreak(elements, b) ? 1 : 0;
    if (h > config.hyphenBudget) continue;
    const k = key(-1, b, h);
    const cost = lineCost(m, config);
    const existing = entries.get(k);
    if (!existing || cost < existing.cost - EPS) {
      entries.set(k, { a: -1, b, h, cost, predKey: null });
    }
  }

  // 按断点位置升序松弛; 转移只走向更大的断点, 单趟即可完成
  for (let idx = 0; idx < positions.length; idx++) {
    const b = positions[idx];
    const here: Array<[string, Entry]> = [];
    for (const [k, e] of entries) if (e.b === b) here.push([k, e]);
    for (const [k, e] of here) {
      const m1 = measure(e.a, b);
      const flagB = isHyphenBreak(elements, b);
      for (let j = idx + 1; j < positions.length; j++) {
        const q = positions[j];
        const m2 = measure(b, q);
        if (!m2.feasible) continue;
        const flagQ = isHyphenBreak(elements, q);
        if (flagB && flagQ) continue; // 禁止连续断词
        const h2 = e.h + (flagQ ? 1 : 0);
        if (h2 > config.hyphenBudget) continue; // 跨行断词预算
        const adj = config.adjacencyWeight * Math.abs(m1.remaining - m2.remaining);
        const cost = e.cost + lineCost(m2, config) + adj;
        const k2 = key(b, q, h2);
        const existing = entries.get(k2);
        if (better(cost, b, existing)) {
          entries.set(k2, { a: b, b: q, h: h2, cost, predKey: k });
        }
      }
    }
  }
  return { entries };
}

export interface LineEvidence {
  index: number;
  measure: LineMeasure;
  adjPenalty: number;
  cost: number;
  cumulative: number;
}

export interface LayoutResult {
  cost: number;
  /** 断点元素下标 (不含段尾) */
  breaks: number[];
  lines: LineEvidence[];
  hyphenCount: number;
  statesExplored: number;
}

export function bestResult(elements: Element[], config: LayoutConfig, table: Table): LayoutResult {
  const end = elements.length;
  let bestKey: string | null = null;
  let best: Entry | null = null;
  for (const [k, e] of table.entries) {
    if (e.b !== end) continue;
    if (
      best === null ||
      e.cost < best.cost - EPS ||
      (Math.abs(e.cost - best.cost) <= EPS && (e.a < best.a || (e.a === best.a && e.h < best.h)))
    ) {
      best = e;
      bestKey = k;
    }
  }
  if (best === null || bestKey === null) throw new Error('无可行排版');

  const breaksReversed: number[] = [];
  let cursor: Entry = best;
  while (cursor.predKey !== null) {
    breaksReversed.push(cursor.a);
    const pred = table.entries.get(cursor.predKey);
    if (!pred) throw new Error('DP 表前驱链断裂');
    cursor = pred;
  }
  const breaks = breaksReversed.reverse();
  return { ...buildEvidence(elements, config, breaks), cost: best.cost, statesExplored: table.entries.size };
}

/** 由断点序列重建每行度量与代价分解 (决策证据)。 */
export function buildEvidence(
  elements: Element[],
  config: LayoutConfig,
  breaks: number[],
): Omit<LayoutResult, 'cost' | 'statesExplored'> {
  const lines: LineEvidence[] = [];
  let cumulative = 0;
  let prevRemaining: number | null = null;
  let from = -1;
  for (const to of [...breaks, elements.length]) {
    const m = measureLine(elements, from, to, config);
    const adjPenalty = prevRemaining === null ? 0 : config.adjacencyWeight * Math.abs(prevRemaining - m.remaining);
    const cost = lineCost(m, config) + adjPenalty;
    cumulative += cost;
    lines.push({ index: lines.length, measure: m, adjPenalty, cost, cumulative });
    prevRemaining = m.remaining;
    from = to;
  }
  const hyphenCount = lines.filter((l) => l.measure.hyphenated).length;
  return { breaks, lines, hyphenCount };
}

export function layoutOptimal(elements: Element[], config: LayoutConfig): LayoutResult {
  return bestResult(elements, config, solveTable(elements, config));
}
