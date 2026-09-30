/** 输入节点树模型: 全部由夹具给出, 不依赖任何浏览器布局引擎。 */

export interface GlyphMetrics {
  /** 每个字符的字形宽度 (任意单位, 由夹具定义) */
  charWidths: Readonly<Record<string, number>>;
  defaultCharWidth: number;
  /** 断词时补在行尾的连字符宽度 */
  hyphenWidth: number;
  /** 空格的理想宽 / 可拉伸 / 可收缩量 */
  space: { width: number; stretch: number; shrink: number };
}

export interface WordFragment {
  text: string;
  width: number;
}

export interface WordNode {
  kind: 'word';
  text: string;
  width: number;
  /** 断词片段; 不可断词只有一个片段 */
  fragments: WordFragment[];
  /** fragments.length - 1 个, 第 i 个对应第 i 与 i+1 片段之间的断词点 */
  hyphenPenalties: number[];
}

export interface SpaceNode {
  kind: 'space';
  width: number;
  stretch: number;
  shrink: number;
}

export type ParagraphNode = WordNode | SpaceNode;

export interface Paragraph {
  nodes: ParagraphNode[];
}

export interface LayoutConfig {
  lineWidth: number;
  /** 跨行一致的全段落断词次数上限 */
  hyphenBudget: number;
  /** 相邻行剩余宽差值的罚分权重 */
  adjacencyWeight: number;
  /** 不可断长词溢出行的基础罚分 */
  overflowPenalty: number;
  /** 每单位溢出宽度的罚分 */
  overflowWeight: number;
}

export const BASE_CONFIG: LayoutConfig = {
  lineWidth: 100,
  hyphenBudget: 2,
  adjacencyWeight: 1,
  overflowPenalty: 1000,
  overflowWeight: 10,
};
