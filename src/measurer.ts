/**
 * Measurement is caller-provided: the layout engine never decides where
 * grapheme cluster boundaries are or how wide a cluster is. It only
 * consumes `Measurer` output and guarantees clusters stay atomic
 * (never split by soft wrap, hit testing, or offset conversion).
 */

export interface GraphemeMetric {
  /** UTF-16 code-unit length of the cluster. */
  readonly length: number;
  /** Terminal columns occupied: 0 (zero-width/combining), 1, or 2 (wide). */
  readonly width: number;
}

export interface Measurer {
  /**
   * Split one single-line, tab-free segment into grapheme clusters.
   * Tabs and line breaks are handled by the layout engine itself and
   * are never passed here.
   */
  measure(segment: string): GraphemeMetric[];
}

/** Combining marks, format controls and variation selectors: zero columns. */
const ZERO_WIDTH_RE = /[\p{Mn}\p{Me}\p{Cf}︀-️\u{E0100}-\u{E01EF}]/u;

/** East Asian Wide/Fullwidth + emoji presentation ranges: two columns. */
function isWideCodePoint(cp: number): boolean {
  return (
    (cp >= 0x1100 && cp <= 0x115f) || // Hangul Jamo
    (cp >= 0x2e80 && cp <= 0xa4cf) || // CJK radicals .. Yi
    (cp >= 0xa960 && cp <= 0xa97f) || // Hangul Jamo Extended-A
    (cp >= 0xac00 && cp <= 0xd7a3) || // Hangul syllables
    (cp >= 0xf900 && cp <= 0xfaff) || // CJK compatibility ideographs
    (cp >= 0xfe30 && cp <= 0xfe4f) || // CJK compatibility forms
    (cp >= 0xfe60 && cp <= 0xfe6f) || // small form variants
    (cp >= 0xff00 && cp <= 0xff60) || // fullwidth forms
    (cp >= 0xffe0 && cp <= 0xffe6) || // fullwidth signs
    (cp >= 0x1f300 && cp <= 0x1f64f) || // emoji
    (cp >= 0x1f900 && cp <= 0x1f9ff) || // supplemental emoji
    (cp >= 0x20000 && cp <= 0x3fffd) // CJK ext B+
  );
}

/** Width of one cluster = widest code point inside it (combining marks are 0). */
export function clusterWidth(cluster: string): number {
  let width = 0;
  for (const ch of cluster) {
    if (ZERO_WIDTH_RE.test(ch)) continue;
    const cp = ch.codePointAt(0) as number;
    const w = isWideCodePoint(cp) ? 2 : 1;
    if (w > width) width = w;
  }
  return width;
}

/**
 * Default measurer: `Intl.Segmenter` grapheme boundaries +
 * terminal-style column widths (wide = 2, zero-width marks = 0).
 * Callers may supply their own measurer to the layout engine instead.
 */
export function createDefaultMeasurer(): Measurer {
  const segmenter = new Intl.Segmenter(undefined, { granularity: 'grapheme' });
  return {
    measure(segment: string): GraphemeMetric[] {
      const metrics: GraphemeMetric[] = [];
      for (const part of segmenter.segment(segment)) {
        metrics.push({
          length: part.segment.length,
          width: clusterWidth(part.segment),
        });
      }
      return metrics;
    },
  };
}
