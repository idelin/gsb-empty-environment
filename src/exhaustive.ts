import { matchTokens } from './match.ts';
import { computeMergeable } from './moves.ts';
import { buildResult } from './diff.ts';
import type { DiffResult, Token } from './types.ts';

/**
 * Brute-force optimum over all kept/moved classifications of the matched
 * tokens, using the same lexicographic objectives as the DP:
 * (kept count desc, block count asc, moved source sum asc).
 * Exponential — only for small inputs; used to cross-check optimality.
 */
export function diffTokensExhaustive(
  oldTokens: Token[],
  newTokens: Token[],
  maxMatched = 22,
): DiffResult {
  const match = matchTokens(oldTokens, newTokens);
  const src: number[] = [];
  for (let j = 0; j < newTokens.length; j++) {
    if (match.newToOld[j] >= 0) src.push(match.newToOld[j]);
  }
  const k = src.length;
  if (k > maxMatched) {
    throw new Error(`exhaustive search limited to ${maxMatched} matched tokens, got ${k}`);
  }
  const matchedOld = oldTokens.map((_, i) => match.oldToNew[i] >= 0);
  const mergeable = computeMergeable(src, matchedOld);

  let bestKept: boolean[] | null = null;
  let bestKey: [number, number, number] | null = null;
  const total = 1 << k;
  for (let mask = 0; mask < total; mask++) {
    const kept: boolean[] = [];
    let valid = true;
    let lastKept = -1;
    let keptCount = 0;
    for (let i = 0; i < k; i++) {
      const isKept = ((mask >> i) & 1) === 1;
      kept.push(isKept);
      if (isKept) {
        if (src[i] <= lastKept) {
          valid = false;
          break;
        }
        lastKept = src[i];
        keptCount++;
      }
    }
    if (!valid) continue;
    let blockCount = 0;
    let movedSum = 0;
    for (let i = 0; i < k; i++) {
      if (kept[i]) continue;
      movedSum += src[i];
      if (i === 0 || kept[i - 1] || !mergeable[i]) blockCount++;
    }
    const key: [number, number, number] = [keptCount, blockCount, movedSum];
    if (
      bestKey === null ||
      key[0] > bestKey[0] ||
      (key[0] === bestKey[0] && key[1] < bestKey[1]) ||
      (key[0] === bestKey[0] && key[1] === bestKey[1] && key[2] < bestKey[2])
    ) {
      bestKey = key;
      bestKept = kept;
    }
  }

  if (bestKept === null || bestKey === null) throw new Error('unreachable: empty plan set');
  const plan = {
    kept: bestKept,
    keptCount: bestKey[0],
    movedCount: k - bestKey[0],
    blockCount: bestKey[1],
    movedSourceSum: bestKey[2],
    optimal: true,
    budgetExhausted: false,
    nodesExplored: total,
  };
  return buildResult(oldTokens, newTokens, match, plan);
}
