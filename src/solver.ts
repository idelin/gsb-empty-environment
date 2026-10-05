export interface Constraint {
  mask: number;
  need: number;
}

export function popcount(value : number) : number {
  let v = value;
  let count = 0;
  while (v !== 0) {
    v = v & (v - 1);
    count += 1;
  }
  return count;
}

function isFeasible(subset : number, constraints : Constraint[]) : boolean {
  for (const c of constraints) {
    if (popcount(subset & c.mask) < c.need) return false;
  }
  return true;
}

export function findMinSubset(numCandidates : number, constraints : Constraint[]) : number {
  for (const c of constraints) {
    if (popcount(c.mask) < c.need) return -1;
  }
  const limit = 2 ** numCandidates;
  for (let size = 0; size <= numCandidates; size += 1) {
    let subset = (2 ** size) - 1;
    while (subset < limit) {
      if (isFeasible(subset, constraints)) return subset;
      if (subset === 0) break;
      const lowest = subset & (-subset);
      const ripple = subset + lowest;
      subset = (((ripple ^ subset) >>> 2) / lowest) | ripple;
    }
  }
  return -1;
}
