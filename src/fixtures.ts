import type { GlyphMetrics, LayoutConfig, Paragraph, SpaceNode, WordNode } from './model.ts';
import { BASE_CONFIG } from './model.ts';

/** 字形度量夹具: 全部宽度由此给出, 不借浏览器布局引擎。 */
export const GLYPHS: GlyphMetrics = {
  charWidths: {
    a: 8, b: 9, c: 8, d: 9, e: 8, f: 5, g: 9, h: 9, i: 4, j: 4, k: 8, l: 4, m: 13,
    n: 9, o: 9, p: 9, q: 9, r: 6, s: 7, t: 5, u: 9, v: 8, w: 11, x: 8, y: 8, z: 7,
  },
  defaultCharWidth: 8,
  hyphenWidth: 5,
  space: { width: 10, stretch: 6, shrink: 4 },
};

export function wordWidth(text: string): number {
  let width = 0;
  for (const ch of text) width += GLYPHS.charWidths[ch] ?? GLYPHS.defaultCharWidth;
  return width;
}

/** 由文本构造词节点; splits 为断词字符下标 (在该字符前断开)。 */
export function word(text: string, splits: number[] = [], hyphenPenalty = 5): WordNode {
  const points = [...splits].sort((a, b) => a - b);
  const fragments = [];
  let prev = 0;
  for (const p of points) {
    const slice = text.slice(prev, p);
    fragments.push({ text: slice, width: wordWidth(slice) });
    prev = p;
  }
  const rest = text.slice(prev);
  fragments.push({ text: rest, width: wordWidth(rest) });
  return { kind: 'word', text, width: wordWidth(text), fragments, hyphenPenalties: points.map(() => hyphenPenalty) };
}

/** 直接指定宽度的词节点 (用于精确构造代价场景)。 */
export function rawWord(text: string, width: number): WordNode {
  return { kind: 'word', text, width, fragments: [{ text, width }], hyphenPenalties: [] };
}

export function rawHyphenWord(
  text: string,
  fragmentWidths: number[],
  hyphenPenalty: number,
): WordNode {
  const total = fragmentWidths.reduce((s, w) => s + w, 0);
  const fragments = fragmentWidths.map((w, i) => ({ text: `${text}[${i}]`, width: w }));
  return {
    kind: 'word',
    text,
    width: total,
    fragments,
    hyphenPenalties: fragmentWidths.slice(1).map(() => hyphenPenalty),
  };
}

export function paragraph(words: WordNode[], space: SpaceNode | null = null): Paragraph {
  const sp = space ?? { kind: 'space' as const, ...GLYPHS.space };
  const nodes: Paragraph['nodes'] = [];
  words.forEach((w, i) => {
    if (i > 0) nodes.push({ ...sp });
    nodes.push(w);
  });
  return { nodes };
}

export const TIGHT_SPACE: SpaceNode = { kind: 'space', width: 10, stretch: 10, shrink: 5 };

export interface Fixture {
  name: string;
  paragraph: Paragraph;
  config: LayoutConfig;
}

/** 普通小段落: 可做穷举对照。 */
export const SHORT: Fixture = {
  name: '短段落',
  paragraph: paragraph(
    ['the', 'quick', 'brown', 'fox', 'jumps', 'over', 'the', 'lazy', 'dog'].map((t) => word(t)),
  ),
  config: { ...BASE_CONFIG, lineWidth: 120, adjacencyWeight: 0 },
};

/** 贪心陷阱: 贪心把首行塞满 (劣度 1), 全局最优是首行只放一词 (总代价更低)。 */
export const GREEDY_TRAP: Fixture = {
  name: '贪心陷阱',
  paragraph: paragraph(
    [rawWord('wide', 60), rawWord('mid', 35), rawWord('tiny', 20), rawWord('last', 20)],
    TIGHT_SPACE,
  ),
  config: { ...BASE_CONFIG, lineWidth: 100, hyphenBudget: 0, adjacencyWeight: 0 },
};

/** 相同代价: [80][12,30] 与 [80,12][30] 同为 0.16, 按较早断点决胜。 */
export const TIE: Fixture = {
  name: '同代价决胜',
  paragraph: paragraph([rawWord('alpha', 80), rawWord('be', 12), rawWord('cat', 30)], TIGHT_SPACE),
  config: { ...BASE_CONFIG, lineWidth: 100, hyphenBudget: 0, adjacencyWeight: 0 },
};

/** 断词场景: 词超行宽, 不断词只能溢出, 逼出断词/连续断词约束。 */
export const HYPHEN: Fixture = {
  name: '断词约束',
  paragraph: paragraph(
    [
      rawHyphenWord('alpha', [40, 35], 1),
      rawHyphenWord('bravo', [40, 35], 1),
      rawHyphenWord('charlie', [40, 35], 1),
    ],
    TIGHT_SPACE,
  ),
  config: { ...BASE_CONFIG, lineWidth: 60, hyphenBudget: 3, adjacencyWeight: 0 },
};

/** 不可断长词: 宽度 250 超行宽 100, 允许溢出但必须报告, 且不能死循环。 */
export const LONG_WORD: Fixture = {
  name: '不可断长词',
  paragraph: paragraph([word('a'), rawWord('superlongunbreakableword', 250), word('day')]),
  config: { ...BASE_CONFIG, lineWidth: 100, hyphenBudget: 0, adjacencyWeight: 0 },
};

/** 邻行宽度差罚分: 权重 0 时全单行, 权重大时趋向等剩余宽。 */
export const ADJACENCY: Fixture = {
  name: '邻行宽度差',
  paragraph: paragraph(
    [44, 31, 27, 38, 25, 33].map((w, i) => rawWord(`w${i}`, w)),
    TIGHT_SPACE,
  ),
  config: { ...BASE_CONFIG, lineWidth: 100, hyphenBudget: 0, adjacencyWeight: 0 },
};
