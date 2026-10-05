import type { PathRequirement } from "./types.ts";

export interface Constraint {
  fileIndex: number;
  requirement: PathRequirement;
  /** Approvals from the owner set that are already given. */
  base: number;
  /** Indices into the candidate array whose users own this requirement. */
  ownerCandidates: number[];
}

/**
 * Find a minimum-size subset of `candidates` such that every constraint
 * reaches its threshold. Returns null when even all candidates together
 * cannot satisfy every constraint.
 *
 * Iterative deepening on the subset size with branch-and-bound pruning;
 * the candidate list is capped at 20 entries by the caller.
 */
export function solveMinimumSet(
  candidates: string[],
  constraints: Constraint[],
): number[] | null {
  const open = constraints.filter((c) => c.base < c.requirement.threshold);
  if (open.length === 0) return [];

  const n = candidates.length;
  const memberOf: number[][] = candidates.map(() => []);
  open.forEach((constraint, ci) => {
    for (const candIdx of constraint.ownerCandidates) memberOf[candIdx].push(ci);
  });

  // Suffix coverage: cover[i][ci] = owners of constraint ci among candidates[i..].
  const cover: number[][] = Array.from({ length: n + 1 }, () =>
    new Array<number>(open.length).fill(0),
  );
  for (let i = n - 1; i >= 0; i--) {
    for (let ci = 0; ci < open.length; ci++) cover[i][ci] = cover[i + 1][ci];
    for (const ci of memberOf[i]) cover[i][ci] += 1;
  }

  // Infeasible when a constraint cannot be met even with every candidate.
  for (let ci = 0; ci < open.length; ci++) {
    if (open[ci].base + cover[0][ci] < open[ci].requirement.threshold) {
      return null;
    }
  }

  const counts = open.map((c) => c.base);
  const deficits = open.map((c, ci) => c.requirement.threshold - counts[ci]);

  let lowerBound = 0;
  for (const deficit of deficits) lowerBound = Math.max(lowerBound, deficit);

  const search = (start: number, slotsLeft: number): number[] | null => {
    if (deficits.every((d) => d <= 0)) return [];
    if (slotsLeft === 0 || start >= n) return null;
    for (let ci = 0; ci < open.length; ci++) {
      if (deficits[ci] > cover[start][ci]) return null;
    }
    // Include candidates[start].
    for (const ci of memberOf[start]) deficits[ci] -= 1;
    const withCurrent = search(start + 1, slotsLeft - 1);
    for (const ci of memberOf[start]) deficits[ci] += 1;
    if (withCurrent !== null) return [start, ...withCurrent];
    // Skip candidates[start].
    return search(start + 1, slotsLeft);
  };

  for (let size = lowerBound; size <= n; size++) {
    const result = search(0, size);
    if (result !== null) return result;
  }
  return null;
}
