import { breakPositions, measureLine } from './nodes.ts';
import { lineCost } from './cost.ts';
import type { BreakOptions, Node } from './types.ts';
import type { LineInfo } from './breaker.ts';

export interface GreedyResult {
  /** NEVER call this optimal: greedy is a local first-fit heuristic. */
  heuristic: 'greedy-first-fit (local, NOT optimal)';
  totalCost: number;
  breaks: number[];
  lines: LineInfo[];
}

/**
 * First-fit greedy: take break positions in order, extend the current line
 * while it still fits (natural width <= lineWidth), break otherwise.
 * A word wider than the line forms its own (overflowing) line: no loop.
 */
export function greedyBreak(nodes: Node[], options: BreakOptions): GreedyResult {
  const positions = breakPositions(nodes);
  const breaks: number[] = [0];
  let i = 0;
  while (i < positions.length - 1) {
    const a = positions[i];
    let chosen = i + 1;
    for (let m = i + 1; m < positions.length; m++) {
      const line = measureLine(nodes, a, positions[m]);
      if (!line.hasContent) continue;
      if (line.naturalWidth <= options.lineWidth) chosen = m;
      else break;
    }
    breaks.push(positions[chosen]);
    i = chosen;
  }
  const lines: LineInfo[] = [];
  let cumulative = 0;
  let prevWidth: number | null = null;
  let hyphens = 0;
  for (let k = 1; k < breaks.length; k++) {
    const a = breaks[k - 1];
    const b = breaks[k];
    const line = measureLine(nodes, a, b);
    if (line.hyphenated) hyphens++;
    const tail = nodes[b - 1];
    const pen = tail && tail.kind === 'penalty' ? tail.penalty : 0;
    const c = lineCost(line, pen, prevWidth, b === nodes.length, options);
    cumulative += c.total;
    lines.push({
      ...c,
      index: k - 1,
      startBreak: a,
      endBreak: b,
      text: line.text,
      naturalWidth: line.naturalWidth,
      remaining: options.lineWidth - line.naturalWidth,
      hyphenated: line.hyphenated,
      atomic: line.atomic,
      cumulativeCost: cumulative,
    });
    prevWidth = line.naturalWidth;
  }
  return {
    heuristic: 'greedy-first-fit (local, NOT optimal)',
    totalCost: cumulative,
    breaks,
    lines,
  };
}
