/**
 * Caller-provided measurement contract.
 *
 * The layout engine never segments text itself: it consumes grapheme
 * clusters (boundaries + column widths) supplied by a `Measurer`.
 * Clusters are atomic for the engine — a combining sequence is never
 * split across columns, rows, or caret positions.
 */

export interface ClusterInfo {
  /** UTF-16 offset of the cluster within the measured line. */
  readonly offset: number;
  /** UTF-16 length of the cluster (>= 1). */
  readonly length: number;
  /**
   * Column width: 0 for zero-width (pure combining/control) clusters,
   * 1 for narrow, 2 for wide. A width of -1 marks a TAB cluster whose
   * width is computed dynamically by the layout (next tab stop).
   */
  readonly width: number;
}

export interface Measurer {
  /** Segment one logical line (no line terminators) into clusters. */
  measure(line: string): ClusterInfo[];
}

const COMBINING_RE = /\p{M}/u;

function isWideCodePoint(cp: number): boolean {
  return (
    cp >= 0x1100 &&
    (cp <= 0x115f || // Hangul Jamo
      (cp >= 0x2e80 && cp <= 0x303e) || // CJK Radicals .. CJK Symbols
      (cp >= 0x3041 && cp <= 0x33ff) || // Hiragana .. CJK Compatibility
      (cp >= 0x3400 && cp <= 0x4dbf) || // CJK Ext A
      (cp >= 0x4e00 && cp <= 0x9fff) || // CJK Unified
      (cp >= 0xa000 && cp <= 0xa4cf) || // Yi
      (cp >= 0xac00 && cp <= 0xd7a3) || // Hangul Syllables
      (cp >= 0xf900 && cp <= 0xfaff) || // CJK Compatibility Ideographs
      (cp >= 0xfe30 && cp <= 0xfe6f) || // CJK Compatibility Forms
      (cp >= 0xff00 && cp <= 0xff60) || // Fullwidth Forms
      (cp >= 0xffe0 && cp <= 0xffe6) || // Fullwidth signs
      (cp >= 0x1f300 && cp <= 0x1f64f) || // Emoji
      (cp >= 0x1f900 && cp <= 0x1f9ff) || // Supplemental Symbols
      (cp >= 0x20000 && cp <= 0x3fffd)) // CJK Ext B+
  );
}

function codePointWidth(cp: number): number {
  if (cp === 0x200d) return 0; // ZWJ (cluster-level handling below)
  if (cp === 0xfe0e || cp === 0xfe0f) return 0; // variation selectors
  if (cp < 0x20 || (cp >= 0x7f && cp < 0xa0)) return 0; // control chars
  if (COMBINING_RE.test(String.fromCodePoint(cp))) return 0; // Mn/Me/…
  return isWideCodePoint(cp) ? 2 : 1;
}

/** Width of one grapheme cluster in terminal columns. */
export function clusterWidth(cluster: string): number {
  if (cluster === '\t') return -1; // tab: expanded by the layout engine
  if (cluster.includes('‍')) return 2; // ZWJ emoji sequence renders wide
  let width = 0;
  for (const ch of cluster) {
    width += codePointWidth(ch.codePointAt(0)!);
  }
  return width;
}

/**
 * Default measurer: `Intl.Segmenter` grapheme boundaries + East Asian
 * width. Callers with a real shaper (e.g. a terminal cell database) can
 * supply their own `Measurer` instead.
 */
export class GraphemeMeasurer implements Measurer {
  private readonly segmenter = new Intl.Segmenter(undefined, {
    granularity: 'grapheme',
  });

  measure(line: string): ClusterInfo[] {
    const clusters: ClusterInfo[] = [];
    for (const seg of this.segmenter.segment(line)) {
      clusters.push({
        offset: seg.index,
        length: seg.segment.length,
        width: clusterWidth(seg.segment),
      });
    }
    return clusters;
  }
}
