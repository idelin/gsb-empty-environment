/**
 * Line breaking, box construction and the visual-caret map.
 *
 * Offsets are grapheme-cluster offsets: offset k is the boundary just
 * before cluster k, so valid offsets are 0..clusters.length.
 */

import type { Box, Caret, Cluster, LayoutSnapshot, LineBias, VisualLine } from './types.ts';
import { dirOf } from './types.ts';
import { reorderByLevels } from './reorder.ts';

const LINE_HEIGHT = 1;

interface LogicalLine {
  clusters: Cluster[];
  /** boundary offsets: empty lines (hard breaks) keep their real position */
  startOffset: number;
  endOffset: number;
}

/** Greedy word-less wrapping on cluster widths; '\n' forces a break. */
export function breakLines(clusters: readonly Cluster[], lineWidth: number): LogicalLine[] {
  const lines: LogicalLine[] = [];
  let current: Cluster[] = [];
  let width = 0;
  let position = 0;
  let lineStart = 0;

  const flush = (nextStart: number): void => {
    const end = current.length > 0 ? current[current.length - 1]!.index + 1 : lineStart;
    lines.push({ clusters: current, startOffset: lineStart, endOffset: end });
    current = [];
    width = 0;
    lineStart = nextStart;
  };

  for (const cluster of clusters) {
    if (cluster.hardBreak) {
      flush(position + 1);
      position += 1;
      continue;
    }
    if (current.length > 0 && width + cluster.width > lineWidth) flush(position);
    current.push(cluster);
    width += cluster.width;
    position += 1;
  }
  flush(clusters.length);
  return lines;
}

function makeBoxes(line: LogicalLine): Box[] {
  const levels = line.clusters.map((c) => c.level);
  const visualOrder = reorderByLevels(levels);
  const boxes: Box[] = [];
  let x = 0;
  for (const visualPos of visualOrder) {
    const cluster = line.clusters[visualPos]!;
    boxes.push({
      clusterIndex: cluster.index,
      text: cluster.text,
      level: cluster.level,
      dir: dirOf(cluster.level),
      width: cluster.width,
      x,
    });
    x += cluster.width;
  }
  return boxes;
}

interface EdgeEvent {
  kind: 'end' | 'start';
  x: number;
  offset: number;
  edge: 'leading' | 'trailing';
  level: number;
}

function lineCarets(
  lineIndex: number,
  line: LogicalLine,
  boxes: readonly Box[],
  version: number,
): Caret[] {
  const events: EdgeEvent[] = [];
  for (const box of boxes) {
    if (box.dir === 'ltr') {
      events.push(
        { kind: 'start', x: box.x, offset: box.clusterIndex, edge: 'leading', level: box.level },
        { kind: 'end', x: box.x + box.width, offset: box.clusterIndex + 1, edge: 'trailing', level: box.level },
      );
    } else {
      events.push(
        // visual left of an rtl box = its trailing edge (offset n+1)
        { kind: 'end', x: box.x, offset: box.clusterIndex + 1, edge: 'trailing', level: box.level },
        // leading edge (offset n) is on the visual right
        { kind: 'start', x: box.x + box.width, offset: box.clusterIndex, edge: 'leading', level: box.level },
      );
    }
  }
  // Visual order at the same x: end-edge first, then start-edge.
  events.sort((a, b) => a.x - b.x || (a.kind === b.kind ? 0 : a.kind === 'end' ? -1 : 1));

  const carets: Caret[] = [];
  let previous: Caret | null = null;
  for (const event of events) {
    let lineBias: LineBias;
    if (event.offset === line.endOffset && line.endOffset !== line.startOffset) lineBias = 'upper';
    else lineBias = 'lower';

    const caret: Caret = {
      version,
      line: lineIndex,
      offset: event.offset,
      x: event.x,
      affinity: { edge: event.edge, level: event.level },
      lineBias,
    };
    // Collapse the two edges of one logical boundary that land on the same
    // physical position (a leading/trailing pair). Same-edge carets sharing
    // an x are different offsets (e.g. an upper-line leading wrap-home next
    // to the line's offset-0 leading caret) and must both remain.
    if (
      previous &&
      previous.offset === caret.offset &&
      previous.x === caret.x &&
      previous.affinity.edge !== caret.affinity.edge
    ) {
      continue;
    }
    carets.push(caret);
    previous = caret;
  }
  return carets;
}

export function layoutClusters(clusters: readonly Cluster[], lineWidth: number, version: number): LayoutSnapshot {
  const logicalLines = breakLines(clusters, lineWidth);
  const lines: VisualLine[] = [];
  const allCarets: Caret[] = [];
  const lineRanges: Array<{ start: number; end: number }> = [];

  logicalLines.forEach((line, lineIndex) => {
    const boxes = line.clusters.length > 0 ? makeBoxes(line) : [];
    const width = boxes.reduce((sum, box) => sum + box.width, 0);
    lines.push({
      index: lineIndex,
      boxes,
      startOffset: line.startOffset,
      endOffset: line.endOffset,
      width,
      y: lineIndex * LINE_HEIGHT,
      height: LINE_HEIGHT,
    });
    lineRanges.push({ start: line.startOffset, end: line.endOffset });
    allCarets.push(...lineCarets(lineIndex, line, boxes, version));
  });

  return { version, clusters: clusters.slice(), lines, carets: allCarets, lineRanges };
}

export { LINE_HEIGHT };
