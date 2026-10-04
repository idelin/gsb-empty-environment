import type { Equality, Token } from './types.ts';

export interface MatchResult {
  /** oldToNew[i] = index in the new sequence matched to old token i, or -1 (deleted). */
  oldToNew: number[];
  /** newToOld[j] = index in the old sequence matched to new token j, or -1 (inserted). */
  newToOld: number[];
}

/**
 * Matches the two token sequences with a maximum-cardinality, deterministic
 * pairing: equal tokens are paired by occurrence order (the k-th old
 * occurrence of a value pairs with the k-th new occurrence). Every matched
 * token is "preserved" — it will be classified as kept or moved by the move
 * planner — while unmatched old tokens are deletions and unmatched new tokens
 * are insertions. This is what lets the differ report a cut-and-pasted block
 * as a move instead of a delete plus a re-insert.
 *
 * Pairing by occurrence order is stable (it never crosses pairs of the same
 * value) and achieves the maximum possible number of matched tokens.
 */
export function matchTokens(
  oldTokens: Token[],
  newTokens: Token[],
  equals?: Equality,
): MatchResult {
  const n = oldTokens.length;
  const m = newTokens.length;
  const oldToNew = new Array<number>(n).fill(-1);
  const newToOld = new Array<number>(m).fill(-1);

  if (!equals) {
    // Fast path: hash grouping by token value.
    const newOccurrences = new Map<Token, number[]>();
    for (let j = 0; j < m; j++) {
      const list = newOccurrences.get(newTokens[j]);
      if (list) list.push(j);
      else newOccurrences.set(newTokens[j], [j]);
    }
    for (let i = 0; i < n; i++) {
      const list = newOccurrences.get(oldTokens[i]);
      if (list && list.length > 0) {
        const j = list.shift() as number;
        oldToNew[i] = j;
        newToOld[j] = i;
      }
    }
    return { oldToNew, newToOld };
  }

  // General path: custom equality, group by representative token.
  const groups: Array<{ rep: Token; oldIdx: number[]; newIdx: number[] }> = [];
  const groupOf = (t: Token): number => groups.findIndex((g) => equals(g.rep, t));
  for (let i = 0; i < n; i++) {
    const g = groupOf(oldTokens[i]);
    if (g >= 0) groups[g].oldIdx.push(i);
    else groups.push({ rep: oldTokens[i], oldIdx: [i], newIdx: [] });
  }
  for (let j = 0; j < m; j++) {
    const g = groupOf(newTokens[j]);
    if (g >= 0) groups[g].newIdx.push(j);
    else groups.push({ rep: newTokens[j], oldIdx: [], newIdx: [j] });
  }
  for (const g of groups) {
    const pairs = Math.min(g.oldIdx.length, g.newIdx.length);
    for (let p = 0; p < pairs; p++) {
      oldToNew[g.oldIdx[p]] = g.newIdx[p];
      newToOld[g.newIdx[p]] = g.oldIdx[p];
    }
  }
  return { oldToNew, newToOld };
}
