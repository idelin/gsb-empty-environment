import type { LineMetrics } from './nodes.ts';
import {
  NO_STRETCH_BADNESS,
  OVERFLOW_PENALTY,
  OVERFLOW_PER_UNIT,
  type BreakOptions,
} from './types.ts';

export interface LineCost {
  badness: number;
  hyphenCost: number;
  widthDiffCost: number;
  overflowCost: number;
  overflow: boolean;
  total: number;
}

/**
 * Cost of one line. Shared by the DP breaker, the greedy heuristic and the
 * exhaustive checker so all three speak the same numbers.
 *
 * - remaining >= 0: stretch badness 100*(remaining/stretch)^3; a final line
 *   (isLast) stretches for free, like TeX's \parfillskip.
 * - remaining < 0 within shrink: shrink badness.
 * - remaining < -shrink: overflow. Always feasible (an unbreakable word may
 *   overflow) but heavily penalised and reported by callers.
 */
export function lineCost(
  line: LineMetrics,
  hyphenPenalty: number,
  prevNaturalWidth: number | null,
  isLast: boolean,
  options: BreakOptions,
): LineCost {
  const remaining = options.lineWidth - line.naturalWidth;
  let badness = 0;
  let overflowCost = 0;
  let overflow = false;
  if (remaining >= 0) {
    if (isLast) {
      badness = 0;
    } else if (remaining === 0) {
      badness = 0;
    } else if (line.stretch > 0) {
      const ratio = remaining / line.stretch;
      badness = Math.min(NO_STRETCH_BADNESS, Math.round(100 * ratio ** 3));
    } else {
      badness = NO_STRETCH_BADNESS;
    }
  } else {
    const need = -remaining;
    if (need <= line.shrink) {
      const ratio = need / line.shrink;
      badness = Math.round(100 * ratio ** 3);
    } else {
      overflow = true;
      overflowCost = OVERFLOW_PENALTY + OVERFLOW_PER_UNIT * (need - line.shrink);
    }
  }
  const hyphenCost = line.hyphenated ? hyphenPenalty : 0;
  const widthDiffCost =
    prevNaturalWidth === null
      ? 0
      : options.widthDiffWeight * Math.abs(line.naturalWidth - prevNaturalWidth);
  return {
    badness,
    hyphenCost,
    widthDiffCost,
    overflowCost,
    overflow,
    total: badness + hyphenCost + widthDiffCost + overflowCost,
  };
}
