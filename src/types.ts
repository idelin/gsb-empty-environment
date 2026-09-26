/** 核心类型：字体、样式、字形簇、测量结果、节点树。 */

export interface FontFace {
  readonly id: string;
  readonly family: string;
  /** 字体版本，热更新时单调递增。 */
  readonly version: number;
  readonly unitsPerEm: number;
  /** 字体单位下的 ascent/descent。 */
  readonly ascent: number;
  readonly descent: number;
  /** 字符 -> 字体单位下的 advance。 */
  readonly glyphs: Readonly<Record<string, number>>;
}

export interface StyleSpec {
  readonly fontId: string;
  readonly fontSize: number;
  /** 字距（px）。相同文本不同字距必须产生不同测量结果。 */
  readonly letterSpacing: number;
}

export interface GlyphCluster {
  readonly text: string;
  readonly fontId: string;
  readonly fontVersion: number;
  /** 每个字形的 advance（字体单位）。 */
  readonly advances: readonly number[];
}

export interface MeasureResult {
  readonly width: number;
  readonly height: number;
  readonly ascent: number;
  readonly descent: number;
}

export type MeasurePhase = 'estimate' | 'final';

export interface MeasureEvent {
  readonly phase: MeasurePhase;
  readonly key: string;
  readonly result: MeasureResult;
  readonly generation: number;
}

export interface TextNode {
  readonly kind: 'text';
  readonly text: string;
  readonly style: StyleSpec;
}

export interface BoxNode {
  readonly kind: 'box';
  readonly children: readonly LayoutNode[];
}

export type LayoutNode = TextNode | BoxNode;
