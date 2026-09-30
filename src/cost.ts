import type { Element } from './elements.ts';
import type { LayoutConfig } from './model.ts';

export interface LineMeasure {
  /** 行首之前的断点 (-1 表示段首) */
  from: number;
  /** 行尾断点 (elements.length 表示段尾) */
  to: number;
  text: string;
  natural: number;
  stretch: number;
  shrink: number;
  /** lineWidth - natural; 负值即负剩余宽 */
  remaining: number;
  badness: number;
  hyphenated: boolean;
  hyphenPenalty: number;
  /** 超出可用收缩的宽度, 仅不可断长词允许 > 0 */
  overflow: number;
  feasible: boolean;
  isLast: boolean;
  boxCount: number;
}

/**
 * 度量从 from 到 to 的一行。
 * 断在胶: 该胶消失; 断在罚点: 行尾补连字符宽; 段尾: 最后一行剩余宽 >= 0 时劣度为 0。
 */
export function measureLine(
  elements: Element[],
  from: number,
  to: number,
  config: LayoutConfig,
): LineMeasure {
  const isLast = to === elements.length;
  const contentEnd = isLast ? elements.length - 1 : to - 1;
  let natural = 0;
  let stretch = 0;
  let shrink = 0;
  let boxCount = 0;
  let glueCount = 0;
  const parts: string[] = [];
  for (let i = from + 1; i <= contentEnd; i++) {
    const el = elements[i];
    if (el.kind === 'box') {
      natural += el.width;
      boxCount++;
      parts.push(el.text);
    } else if (el.kind === 'glue') {
      natural += el.width;
      stretch += el.stretch;
      shrink += el.shrink;
      glueCount++;
      parts.push(' ');
    }
  }
  let hyphenated = false;
  let hyphenPenalty = 0;
  if (!isLast) {
    const br = elements[to];
    if (br.kind === 'penalty') {
      natural += br.width;
      hyphenated = true;
      hyphenPenalty = br.penalty;
      parts.push(br.text);
    }
  }
  const remaining = config.lineWidth - natural;
  let badness = 0;
  let overflow = 0;
  let feasible = true;
  if (boxCount === 0) {
    feasible = false;
  } else if (remaining >= 0) {
    if (isLast) {
      badness = 0; // 最后一行有无限拉伸
    } else if (stretch > 0) {
      const ratio = remaining / stretch;
      badness = ratio * ratio;
    } else {
      // 无伸缩回退: 以半行宽为拉伸单位估算, 保证可比较且可行
      const ratio = remaining / (config.lineWidth / 2);
      badness = ratio * ratio;
    }
  } else {
    const need = -remaining;
    if (need <= shrink) {
      const ratio = need / shrink;
      badness = ratio * ratio;
    } else if (glueCount === 0) {
      // 不可断长词 (行内无空格, 即单个词): 允许溢出 (罚分见 lineCost), 必须报告
      overflow = need - shrink;
    } else {
      feasible = false;
    }
  }
  return {
    from,
    to,
    text: parts.join(''),
    natural,
    stretch,
    shrink,
    remaining,
    badness,
    hyphenated,
    hyphenPenalty,
    overflow,
    feasible,
    isLast,
    boxCount,
  };
}

/** 单行代价 = 劣度 + 断词罚分 + 溢出罚分; 不可行为 +Infinity。 */
export function lineCost(m: LineMeasure, config: LayoutConfig): number {
  if (!m.feasible) return Infinity;
  let cost = m.badness + (m.hyphenated ? m.hyphenPenalty : 0);
  if (m.overflow > 0) cost += config.overflowPenalty + config.overflowWeight * m.overflow;
  return cost;
}
