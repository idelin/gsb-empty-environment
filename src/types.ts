/** Glyph metrics and the document node tree are supplied by fixtures;
 *  no browser layout engine is involved in any core computation. */
export interface GlyphMetrics {
  charWidths: Record<string, number>;
  defaultCharWidth: number;
  spaceWidth: number;
  lineHeight: number;
  headingHeight: number;
  maxLineWidth: number;
}

export type BlockNode =
  | { kind: 'paragraph'; id: string; text: string }
  | { kind: 'heading'; id: string; text: string }
  | { kind: 'block'; id: string; height: number }; // unbreakable

export interface Document {
  id: string;
  blocks: BlockNode[];
  footnotes: Record<string, { text: string }>;
}
