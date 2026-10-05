import type { Block } from "./types.ts";

export interface CandidateOptions {
  /** Minimum length of a candidate block. Defaults to 1. */
  minLength?: number;
  /**
   * Cap on the number of candidates. When the full set of common blocks
   * exceeds the cap, the candidate set falls back to maximal blocks plus
   * (when minLength allows) all singleton matches, which still preserves
   * optimal token coverage. Defaults to 25000.
   */
  maxCandidates?: number;
}

function byPosition(a: Block, b: Block): number {
  return a.oldStart - b.oldStart || a.newStart - b.newStart || b.length - a.length;
}

/**
 * Generate candidate blocks: pairs of intervals with identical token content.
 *
 * Primary mode returns every common block (with length >= minLength), which
 * includes nested and overlapping candidates arising from repeated tokens.
 * For pathologically repetitive inputs the full set can explode; in that
 * case we fall back to maximal blocks plus all singleton matches. The
 * fallback keeps the same optimal coverage: any non-maximal block in a
 * selection can be replaced by its singleton decomposition without
 * changing coverage or introducing conflicts.
 */
export function generateCandidates(
  oldTokens: string[],
  newTokens: string[],
  options: CandidateOptions = {},
): Block[] {
  const minLength = options.minLength ?? 1;
  const maxCandidates = options.maxCandidates ?? 25_000;

  // Maximal runs: start at positions that cannot be extended left.
  const runs: Block[] = [];
  const positionsByToken = new Map<string, number[]>();
  for (let j = 0; j < newTokens.length; j++) {
    const list = positionsByToken.get(newTokens[j]);
    if (list) list.push(j);
    else positionsByToken.set(newTokens[j], [j]);
  }
  for (let i = 0; i < oldTokens.length; i++) {
    const positions = positionsByToken.get(oldTokens[i]);
    if (!positions) continue;
    for (const j of positions) {
      if (i > 0 && j > 0 && oldTokens[i - 1] === newTokens[j - 1]) continue;
      let length = 0;
      while (
        i + length < oldTokens.length &&
        j + length < newTokens.length &&
        oldTokens[i + length] === newTokens[j + length]
      ) {
        length++;
      }
      runs.push({ oldStart: i, newStart: j, length });
    }
  }

  let totalBlocks = 0;
  for (const run of runs) {
    for (let l = Math.max(1, minLength); l <= run.length; l++) {
      totalBlocks += run.length - l + 1;
    }
  }

  if (totalBlocks <= maxCandidates) {
    const all: Block[] = [];
    for (const run of runs) {
      for (let l = run.length; l >= Math.max(1, minLength); l--) {
        for (let offset = 0; offset + l <= run.length; offset++) {
          all.push({
            oldStart: run.oldStart + offset,
            newStart: run.newStart + offset,
            length: l,
          });
        }
      }
    }
    all.sort(byPosition);
    return all;
  }

  // Fallback: maximal blocks plus singleton matches (deduplicated).
  const seen = new Set<number>();
  const fallback: Block[] = [];
  const width = newTokens.length + 1;
  const push = (b: Block) => {
    const key = (b.oldStart * width + b.newStart) * (oldTokens.length + 1) + b.length;
    if (!seen.has(key)) {
      seen.add(key);
      fallback.push(b);
    }
  };
  for (const run of runs) {
    if (run.length >= minLength) push(run);
    if (minLength <= 1) {
      for (let offset = 0; offset < run.length; offset++) {
        push({ oldStart: run.oldStart + offset, newStart: run.newStart + offset, length: 1 });
      }
    }
  }
  fallback.sort(byPosition);
  return fallback;
}
