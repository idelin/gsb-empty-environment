import type { GlyphMetrics, InlineSpan } from "./model.ts";

export interface MeasuredWord {
  text: string;
  width: number;
  footnoteRefs: string[];
}

export interface MeasuredLine {
  words: MeasuredWord[];
  width: number;
  height: number;
  footnoteRefs: string[];
}

export function measureText(text: string, metrics: GlyphMetrics): number {
  let width = 0;
  for (const ch of text) width += metrics.charWidth(ch);
  return width;
}

export function tokenize(spans: InlineSpan[], metrics: GlyphMetrics): MeasuredWord[] {
  const words: MeasuredWord[] = [];
  for (const span of spans) {
    const parts = span.text.split(/\s+/).filter((p) => p.length > 0);
    parts.forEach((part, idx) => {
      const refs: string[] = [];
      if (span.footnoteRef && idx === parts.length - 1) refs.push(span.footnoteRef);
      words.push({ text: part, width: measureText(part, metrics), footnoteRefs: refs });
    });
  }
  return words;
}

export function breakLines(
  spans: InlineSpan[],
  metrics: GlyphMetrics,
  lineWidth: number,
): MeasuredLine[] {
  const words = tokenize(spans, metrics);
  const lines: MeasuredLine[] = [];
  let current: MeasuredWord[] = [];
  let width = 0;
  const flush = () => {
    if (current.length === 0) return;
    const refs = [...new Set(current.flatMap((w) => w.footnoteRefs))];
    lines.push({ words: current, width, height: metrics.lineHeight, footnoteRefs: refs });
    current = [];
    width = 0;
  };
  for (const word of words) {
    const nextWidth = current.length === 0 ? word.width : width + metrics.spaceWidth + word.width;
    if (current.length > 0 && nextWidth > lineWidth) flush();
    current.push(word);
    width = current.length === 1 ? word.width : width + metrics.spaceWidth + word.width;
  }
  flush();
  return lines;
}

export function footnoteHeight(
  text: string,
  metrics: GlyphMetrics,
  lineWidth: number,
): number {
  return breakLines([{ text }], metrics, lineWidth).length * metrics.lineHeight;
}
