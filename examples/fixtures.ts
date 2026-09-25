/**
 * 夹具：节点树与字形度量。
 * 行高由纯算术根据字形度量表与换行规则算出，不借浏览器布局引擎。
 */

export interface TextNode {
  kind: "text";
  text: string;
  /** 行高（倍数），由字体大小决定。 */
  lineHeight: number;
}

export interface RowNode {
  kind: "row";
  padding: number;
  children: TextNode[];
}

export type FixtureNode = RowNode;

/** 字形度量夹具：每个字符的 advance 宽度（px）。 */
export const GLYPH_METRICS: Record<string, number> = {
  " ": 4,
  "default": 8,
  wide: 14, // 中文等宽字符
};

export function glyphAdvance(ch: string): number {
  if (ch === " ") return GLYPH_METRICS[" "];
  const code = ch.codePointAt(0)!;
  if (code > 0x2e7f) return GLYPH_METRICS.wide;
  return GLYPH_METRICS["default"];
}

export function textWidth(text: string): number {
  let w = 0;
  for (const ch of text) w += glyphAdvance(ch);
  return w;
}

/** 由节点树与可用宽度计算行高：按字形宽度贪心换行，取各文本块最大行高 × 行数 + padding。 */
export function measureNode(node: RowNode, maxWidth: number): number {
  let lines = 1;
  let cursor = 0;
  let maxLineHeight = 0;
  for (const child of node.children) {
    maxLineHeight = Math.max(maxLineHeight, child.lineHeight);
    for (const ch of child.text) {
      const w = glyphAdvance(ch);
      if (cursor + w > maxWidth && cursor > 0) {
        lines++;
        cursor = 0;
      }
      cursor += w;
    }
  }
  return lines * maxLineHeight + node.padding * 2;
}

/** 生成演示用节点树夹具。 */
export function makeRowFixture(i: number): RowNode {
  const samples = [
    "short row",
    "a somewhat longer row that may wrap when the viewport is narrow enough",
    "混合 CJK 字符的行会按更宽的字形度量换行处理",
    "row with\nno real newlines but many words " + "word ".repeat(i % 7),
  ];
  return {
    kind: "row",
    padding: 6,
    children: [
      { kind: "text", text: `#${i} ` + samples[i % samples.length], lineHeight: 18 },
    ],
  };
}
