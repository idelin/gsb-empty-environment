export interface GlyphMetrics {
  lineHeight: number;
  spaceWidth: number;
  charWidth(ch: string): number;
}

export interface InlineSpan {
  text: string;
  footnoteRef?: string;
}

export type BlockNode =
  | { kind: "paragraph"; id: string; spans: InlineSpan[] }
  | { kind: "heading"; id: string; text: string; level: number }
  | { kind: "block"; id: string; height: number };

export interface FootnoteDef {
  id: string;
  text: string;
}

export interface Document {
  nodes: BlockNode[];
  footnotes: FootnoteDef[];
}

export interface PageConfig {
  pageHeight: number;
  lineWidth: number;
  orphans: number;
  widows: number;
  keepWithNext: boolean;
  footnoteSeparatorHeight: number;
  paragraphSplitPenalty: number;
  whitespaceWeight: number;
}

export const defaultConfig: PageConfig = {
  pageHeight: 200,
  lineWidth: 480,
  orphans: 2,
  widows: 2,
  keepWithNext: true,
  footnoteSeparatorHeight: 6,
  paragraphSplitPenalty: 25,
  whitespaceWeight: 1,
};
