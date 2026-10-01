import { breakPositions, measureLine } from './nodes.ts';
import { lineCost } from './cost.ts';
import { comparePaths } from './breaker.ts';
import type { BreakOptions, Node } from './types.ts';

export interface ExhaustiveResult {
  optimalCost: number;
  breaks: number[];
  layouts: number;
  /** how many distinct legal layouts attain the optimal cost */
  optimalLayoutCount: number;
}

/**
 * Brute-force enumeration of every legal break sequence, honouring the same
 * history constraints and the same tie-break (equal cost => lexicographically
 * earliest break positions) as the DP. Only for small paragraphs.
 */
export function exhaustiveBreak(nodes: Node[], options: BreakOptions): ExhaustiveResult {
  const positions = breakPositions(nodes);
  if (positions.length > 24) {
    throw new Error(`too many break positions (${positions.length}) for exhaustive search`);
  }
  let bestCost = Infinity;
  let bestPath: number[] = [];
  let layouts = 0;
  let optimalLayoutCount = 0;

  function hyphenPenaltyAt(b: number): number {
    const n = nodes[b - 1];
    return n && n.kind === 'penalty' ? n.penalty : 0;
  }

  function walk(
    k: number,
    cost: number,
    hyphens: number,
    prevHyphenated: boolean,
    prevWidth: number | null,
    path: number[],
  ): void {
    const a = positions[k];
    if (a === nodes.length) {
      layouts++;
      if (cost < bestCost) {
        bestCost = cost;
        bestPath = path;
        optimalLayoutCount = 1;
      } else if (cost === bestCost) {
        optimalLayoutCount++;
        if (comparePaths(path, bestPath) < 0) bestPath = path;
      }
      return;
    }
    for (let m = k + 1; m < positions.length; m++) {
      const b = positions[m];
      const line = measureLine(nodes, a, b);
      if (!line.hasContent) continue;
      const h = hyphens + (line.hyphenated ? 1 : 0);
      if (h > options.maxHyphens) continue;
      if (options.forbidConsecutiveHyphens && prevHyphenated && line.hyphenated) continue;
      const c = lineCost(line, hyphenPenaltyAt(b), prevWidth, b === nodes.length, options);
      walk(m, cost + c.total, h, line.hyphenated, line.naturalWidth, [...path, b]);
    }
  }
  walk(0, 0, 0, false, null, []);
  return { optimalCost: bestCost, breaks: [0, ...bestPath], layouts, optimalLayoutCount };
}
