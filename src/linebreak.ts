import type { GlyphMetrics } from './types.ts';

export interface BrokenLine {
  text: string;
  width: number;
  footnoteRefs: string[];
}

export interface LineBreakResult {
  lines: BrokenLine[];
  overflowWords: string[];
}

interface Word {
  text: string;
  width: number;
  refs: string[];
}

export function textWidth(text: string, metrics: GlyphMetrics): number {
  let width = 0;
  for (const ch of text) width += metrics.charWidths[ch] ?? metrics.defaultCharWidth;
  return width;
}

const MARKER_RE = /\[\^([^\]]+)\]/g;

function tokenize(text: string, metrics: GlyphMetrics): Word[] {
  const words: Word[] = [];
  for (const raw of text.split(/\s+/).filter((s) => s.length > 0)) {
    const refs: string[] = [];
    MARKER_RE.lastIndex = 0;
    const cleaned = raw.replace(MARKER_RE, (_m, id: string) => {
      refs.push(id);
      return '';
    });
    words.push({ text: cleaned, width: textWidth(cleaned, metrics), refs });
  }
  return words;
}

/** Greedy line breaking over fixture metrics. Footnote markers `[^id]` are
 *  zero-width and attributed to the line their word lands on. */
export function breakParagraph(
  text: string,
  metrics: GlyphMetrics,
  maxWidth: number,
): LineBreakResult {
  const words = tokenize(text, metrics);
  const lines: BrokenLine[] = [];
  const overflowWords: string[] = [];
  let current: Word[] = [];
  let currentWidth = 0;
  const flush = () => {
    if (current.length === 0) return;
    lines.push({
      text: current.map((w) => w.text).join(' '),
      width: currentWidth,
      footnoteRefs: current.flatMap((w) => w.refs),
    });
    current = [];
    currentWidth = 0;
  };
  for (const word of words) {
    if (word.width > maxWidth) {
      flush();
      overflowWords.push(word.text);
      lines.push({ text: word.text, width: word.width, footnoteRefs: word.refs });
      continue;
    }
    const needed =
      current.length === 0 ? word.width : currentWidth + metrics.spaceWidth + word.width;
    if (needed <= maxWidth) {
      current.push(word);
      currentWidth = needed;
    } else {
      flush();
      current.push(word);
      currentWidth = word.width;
    }
  }
  flush();
  if (lines.length === 0) lines.push({ text: '', width: 0, footnoteRefs: [] });
  return { lines, overflowWords };
}
