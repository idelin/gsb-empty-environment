import type { Problem } from "./model.ts";

export interface SatisfiableResult {
  status: "ok";
  minFaults: number;
  solutions: string[][];
}

export interface ContradictionResult {
  status: "contradiction";
  minUnsatObservationSubsets: string[][];
}

export type SolveResult = SatisfiableResult | ContradictionResult;

export function edgeBitIndex(links: string[]): Map<string, number> {
  const index = new Map<string, number>();
  links.forEach((id, position) => index.set(id, position));
  return index;
}

function bitIndex(bit: number): number {
  return 31 - Math.clz32(bit);
}

export function pathMask(path: string[], index: Map<string, number>): number {
  let mask = 0;
  for (const id of path) {
    const bit = index.get(id);
    if (bit !== undefined) {
      mask |= 1 << bit;
    }
  }
  return mask;
}

function popcount(value: number): number {
  let count = 0;
  let remaining = value;
  while (remaining !== 0) {
    remaining &= remaining - 1;
    count += 1;
  }
  return count;
}

function maskToIds(mask: number, links: string[]): string[] {
  const ids: string[] = [];
  for (let bit = 0; bit < links.length; bit += 1) {
    if ((mask & (1 << bit)) !== 0) {
      ids.push(links[bit]);
    }
  }
  return ids;
}

function reduceClauses(clauses: number[]): number[] {
  const unique = [...new Set(clauses)].sort(
    (a, b) => popcount(a) - popcount(b),
  );
  const kept: number[] = [];
  for (const clause of unique) {
    if (!kept.some((other) => (other & clause) === other)) {
      kept.push(clause);
    }
  }
  return kept;
}

function greedyHittingSet(clauses: number[]): number {
  let uncovered = [...clauses];
  let chosen = 0;
  while (uncovered.length > 0) {
    const counts = new Map<number, number>();
    for (const clause of uncovered) {
      let rest = clause;
      while (rest !== 0) {
        const bit = rest & -rest;
        rest ^= bit;
        counts.set(bit, (counts.get(bit) ?? 0) + 1);
      }
    }
    let bestBit = 0;
    let bestCount = -1;
    for (const [bit, count] of counts) {
      if (count > bestCount) {
        bestCount = count;
        bestBit = bit;
      }
    }
    chosen |= bestBit;
    uncovered = uncovered.filter((clause) => (clause & bestBit) === 0);
  }
  return chosen;
}

function enumerateMinHittingSets(clauses: number[], upperBound: number): number[] {
  let best = Number.POSITIVE_INFINITY;
  let solutions: number[] = [];
  best = upperBound;

  const search = (uncovered: number[], chosen: number, depth: number): void => {
    if (depth > best) {
      return;
    }
    if (uncovered.length === 0) {
      if (depth < best) {
        best = depth;
        solutions = [chosen];
      } else if (depth === best) {
        solutions.push(chosen);
      }
      return;
    }
    if (depth === best) {
      return;
    }
    let target = uncovered[0];
    for (const clause of uncovered) {
      if (popcount(clause) < popcount(target)) {
        target = clause;
      }
    }
    let remaining = target;
    while (remaining !== 0) {
      const bit = remaining & -remaining;
      remaining ^= bit;
      const next: number[] = [];
      for (const clause of uncovered) {
        if ((clause & bit) === 0) {
          next.push(clause);
        }
      }
      search(next, chosen | bit, depth + 1);
    }
  };

  search(clauses, 0, 0);
  return solutions;
}

function enumerateMinFaultSetsByComplement(
  numCandidates: number,
  clauses: number[],
  upperBound: number,
): number[] {
  const edgeClauses: number[][] = Array.from(
    { length: numCandidates },
    () => [],
  );
  clauses.forEach((clause, clauseIndex) => {
    let rest = clause;
    while (rest !== 0) {
      const bit = rest & -rest;
      rest ^= bit;
      edgeClauses[bitIndex(bit)].push(clauseIndex);
    }
  });
  const sizes = clauses.map((clause) => popcount(clause));
  const counts = new Array<number>(clauses.length).fill(0);

  let bestHealthy = numCandidates - upperBound;
  const healthySets: number[] = [];

  const search = (edge: number, healthy: number, healthySize: number): void => {
    if (healthySize + (numCandidates - edge) < bestHealthy) {
      return;
    }
    if (edge === numCandidates) {
      if (healthySize > bestHealthy) {
        bestHealthy = healthySize;
        healthySets.length = 0;
        healthySets.push(healthy);
      } else if (healthySize === bestHealthy) {
        healthySets.push(healthy);
      }
      return;
    }
    let violated = false;
    for (const clauseIndex of edgeClauses[edge]) {
      counts[clauseIndex] += 1;
      if (counts[clauseIndex] === sizes[clauseIndex]) {
        violated = true;
      }
    }
    if (!violated) {
      search(edge + 1, healthy | (1 << edge), healthySize + 1);
    }
    for (const clauseIndex of edgeClauses[edge]) {
      counts[clauseIndex] -= 1;
    }
    search(edge + 1, healthy, healthySize);
  };

  search(0, 0, 0);

  const fullMask = (1 << numCandidates) - 1;
  return healthySets.map((healthy) => fullMask ^ healthy);
}

interface CoverChoice {
  observationId: string;
  mask: number;
}

function enumerateMinCovers(
  target: number,
  choices: CoverChoice[],
): CoverChoice[][] {
  let best = Number.POSITIVE_INFINITY;
  let solutions: CoverChoice[][] = [];

  const search = (
    uncovered: number,
    available: CoverChoice[],
    chosen: CoverChoice[],
  ): void => {
    if (chosen.length > best) {
      return;
    }
    if (uncovered === 0) {
      if (chosen.length < best) {
        best = chosen.length;
        solutions = [[...chosen]];
      } else if (chosen.length === best) {
        solutions.push([...chosen]);
      }
      return;
    }
    if (chosen.length === best) {
      return;
    }
    let maxCover = 0;
    for (const choice of available) {
      const size = popcount(choice.mask & uncovered);
      if (size > maxCover) {
        maxCover = size;
      }
    }
    if (maxCover === 0) {
      return;
    }
    if (chosen.length + Math.ceil(popcount(uncovered) / maxCover) > best) {
      return;
    }
    const lowestBit = uncovered & -uncovered;
    for (let i = 0; i < available.length; i += 1) {
      const choice = available[i];
      if ((choice.mask & lowestBit) === 0) {
        continue;
      }
      chosen.push(choice);
      search(
        uncovered & ~choice.mask,
        available.filter((_, index) => index !== i),
        chosen,
      );
      chosen.pop();
    }
  };

  search(target, choices, []);
  return solutions;
}

function coreKey(core: string[]): string {
  return [...core].sort().join("");
}

function findMinUnsatObservationSubsets(problem: Problem): string[][] {
  const index = edgeBitIndex(problem.links);
  const okObservations = problem.observations.filter(
    (observation) => observation.status === "ok",
  );
  const failObservations = problem.observations.filter(
    (observation) => observation.status === "fail",
  );

  let bestSize = Number.POSITIVE_INFINITY;
  let cores: string[][] = [];

  for (const fail of failObservations) {
    const target = pathMask(fail.path, index);
    if (target === 0) {
      if (bestSize > 1) {
        bestSize = 1;
        cores = [];
      }
      cores.push([fail.id]);
      continue;
    }
    if (bestSize === 1) {
      continue;
    }
    const choices: CoverChoice[] = [];
    for (const ok of okObservations) {
      const mask = pathMask(ok.path, index) & target;
      if (mask !== 0) {
        choices.push({ observationId: ok.id, mask });
      }
    }
    const covers = enumerateMinCovers(target, choices);
    for (const cover of covers) {
      const size = cover.length + 1;
      const core = [
        fail.id,
        ...cover.map((choice) => choice.observationId).sort(),
      ];
      if (size < bestSize) {
        bestSize = size;
        cores = [core];
      } else if (size === bestSize) {
        cores.push(core);
      }
    }
  }

  const unique = new Map<string, string[]>();
  for (const core of cores) {
    if (!unique.has(coreKey(core))) {
      unique.set(coreKey(core), core);
    }
  }
  cores = [...unique.values()];
  cores.sort((a, b) => coreKey(a).localeCompare(coreKey(b)));
  return cores;
}

export function solve(problem: Problem): SolveResult {
  const index = edgeBitIndex(problem.links);

  let healthyMask = 0;
  for (const observation of problem.observations) {
    if (observation.status === "ok") {
      healthyMask |= pathMask(observation.path, index);
    }
  }

  const clauses: number[] = [];
  for (const observation of problem.observations) {
    if (observation.status !== "fail") {
      continue;
    }
    const clause = pathMask(observation.path, index) & ~healthyMask;
    if (clause === 0) {
      return {
        status: "contradiction",
        minUnsatObservationSubsets: findMinUnsatObservationSubsets(problem),
      };
    }
    clauses.push(clause);
  }

  const reduced = reduceClauses(clauses);

  let candidateMask = 0;
  for (const clause of reduced) {
    candidateMask |= clause;
  }
  const candidates: number[] = [];
  for (let bit = 0; bit < problem.links.length; bit += 1) {
    if ((candidateMask & (1 << bit)) !== 0) {
      candidates.push(bit);
    }
  }
  const remapped = reduced.map((clause) => {
    let rest = clause;
    let mapped = 0;
    while (rest !== 0) {
      const bit = rest & -rest;
      rest ^= bit;
      mapped |= 1 << candidates.indexOf(bitIndex(bit));
    }
    return mapped;
  });

  const greedy = greedyHittingSet(remapped);
  const upperBound = popcount(greedy);

  let minClauseSize = Number.POSITIVE_INFINITY;
  for (const clause of remapped) {
    minClauseSize = Math.min(minClauseSize, popcount(clause));
  }
  const branchEstimate =
    remapped.length === 0 ? 0 : minClauseSize ** upperBound;

  const masks =
    branchEstimate <= 1 << 22
      ? enumerateMinHittingSets(remapped, upperBound)
      : enumerateMinFaultSetsByComplement(
          candidates.length,
          remapped,
          upperBound,
        );

  const solutions = [...new Set(masks)]
    .map((mask) => {
      let original = 0;
      for (let position = 0; position < candidates.length; position += 1) {
        if ((mask & (1 << position)) !== 0) {
          original |= 1 << candidates[position];
        }
      }
      return maskToIds(original, problem.links);
    })
    .sort((a, b) => a.join("").localeCompare(b.join("")));

  return {
    status: "ok",
    minFaults: solutions.length > 0 ? solutions[0].length : 0,
    solutions,
  };
}
