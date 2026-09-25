// Grapheme cluster segmentation via the platform Intl.Segmenter.
// Emoji ZWJ sequences, flags, keycaps and combining-mark sequences are
// returned as single clusters and therefore can never be split.

export interface ClusterSpan { text: string; start: number; end: number }

const segmenter = new Intl.Segmenter('und', { granularity: 'grapheme' });

export function splitClusters(text: string): ClusterSpan[] {
  const out: ClusterSpan[] = [];
  for (const seg of segmenter.segment(text)) {
    out.push({ text: seg.segment, start: seg.index, end: seg.index + seg.segment.length });
  }
  return out;
}
