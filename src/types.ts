/** Font version is bumped on every hot reload of a font family. */
export type FontVersion = number;

/** A font face fixture: metrics are given, never derived from a browser. */
export interface FontFace {
  family: string;
  version: FontVersion;
  unitsPerEm: number;
  ascent: number;
  descent: number;
  lineGap: number;
  /** advance width per glyph, in font units */
  glyphAdvances: Readonly<Record<string, number>>;
  defaultAdvance: number;
}

/** Style combination. letterSpacing is part of the identity of a measurement. */
export interface TextStyle {
  fontFamily: string;
  fontSize: number;
  weight: number;
  /** extra spacing inserted between adjacent glyph clusters, in px */
  letterSpacing: number;
}

export interface GlyphRun {
  text: string;
  width: number;
}

/** A shaped glyph cluster: the atomic unit produced by (fixture) shaping. */
export interface GlyphCluster {
  text: string;
  glyphs: GlyphRun[];
  width: number;
}

export interface Measurement {
  width: number;
  ascent: number;
  descent: number;
  lineHeight: number;
  clusters: GlyphCluster[];
}

/** Canonical cache key for a measurement request. */
export interface MeasureKey {
  text: string;
  style: TextStyle;
  fontVersion: FontVersion;
}

export function styleKey(style: TextStyle): string {
  return `${style.fontFamily}|${style.fontSize}|${style.weight}|ls:${style.letterSpacing}`;
}

export function measureKey(key: MeasureKey): string {
  return `${styleKey(key.style)}|v${key.fontVersion}|${key.text}`;
}

/** Node tree fixtures: layout input, no browser involved. */
export interface TextNode {
  kind: "text";
  id: string;
  text: string;
  style: TextStyle;
}

export interface BoxNode {
  kind: "box";
  id: string;
  /** available inline width in px; wrapping happens against this */
  width: number;
  children: LayoutNode[];
}

export type LayoutNode = TextNode | BoxNode;

export interface LayoutBox {
  id: string;
  width: number;
  height: number;
  lines: number;
  children: LayoutBox[];
}

export type LayoutChannel = "estimate" | "final";

export interface LayoutResult {
  channel: LayoutChannel;
  round: number;
  tree: LayoutBox;
}
