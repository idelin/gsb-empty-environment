/**
 * Statement identity reconciliation between two versions of a source file.
 *
 * Given the keyed statements of the previous version and the keys of the
 * new version (both in document order), identity is propagated as follows:
 *
 *  1. A longest-common-subsequence alignment over the key sequences keeps
 *     identity for statements whose relative order is unchanged. For
 *     duplicated keys (copies) the pairing is positional, so the *first*
 *     occurrence in the old version maps to the *first* occurrence in the
 *     new version: duplicating a statement never steals the original's
 *     identity when the copy is inserted after it.
 *  2. Remaining unmatched old/new statements with equal keys are paired in
 *     order: these are *moved* statements, which keep their identity (and
 *     therefore their breakpoints) at their new location.
 *  3. Still-unmatched old statements are gone (identity retired); still
 *     unmatched new statements receive fresh identities.
 */

export interface KeyedStatement {
  sid: number;
  key: string;
}

export interface ReconcileResult {
  /** For each new statement index, the inherited sid, or null for a fresh one. */
  assignments: Array<number | null>;
  /** Sids of the old version that no longer exist in the new version. */
  removedSids: number[];
}

export function reconcile(oldStatements: KeyedStatement[], newKeys: string[]): ReconcileResult {
  const m = oldStatements.length;
  const n = newKeys.length;

  // LCS lengths over suffixes.
  const dp: number[][] = Array.from({ length: m + 1 }, () => new Array<number>(n + 1).fill(0));
  for (let i = m - 1; i >= 0; i--) {
    for (let j = n - 1; j >= 0; j--) {
      dp[i][j] = oldStatements[i].key === newKeys[j]
        ? dp[i + 1][j + 1] + 1
        : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }

  const assignments: Array<number | null> = new Array<number | null>(n).fill(null);
  const matchedOld = new Array<boolean>(m).fill(false);

  let i = 0;
  let j = 0;
  while (i < m && j < n) {
    if (oldStatements[i].key === newKeys[j]) {
      assignments[j] = oldStatements[i].sid;
      matchedOld[i] = true;
      i++;
      j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      i++;
    } else {
      j++;
    }
  }

  // Pair remaining statements with equal keys: moved statements.
  const unmatchedOldByKey = new Map<string, number[]>();
  for (let a = 0; a < m; a++) {
    if (!matchedOld[a]) {
      const list = unmatchedOldByKey.get(oldStatements[a].key);
      if (list) list.push(oldStatements[a].sid);
      else unmatchedOldByKey.set(oldStatements[a].key, [oldStatements[a].sid]);
    }
  }
  for (let b = 0; b < n; b++) {
    if (assignments[b] === null) {
      const list = unmatchedOldByKey.get(newKeys[b]);
      if (list && list.length > 0) {
        assignments[b] = list.shift() as number;
      }
    }
  }

  const removedSids: number[] = [];
  for (const list of unmatchedOldByKey.values()) removedSids.push(...list);

  return { assignments, removedSids };
}
