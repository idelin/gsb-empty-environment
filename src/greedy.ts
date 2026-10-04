import type { AllocationInput, SolveResult } from './types.js';
import { validateInput } from './validate.js';
import { assembleResult } from './assemble.js';

function compareId(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

export function greedyAllocate(input: AllocationInput): SolveResult {
  validateInput(input);
  const nodes = [...input.nodes].sort((a, b) => compareId(a.id, b.id));
  const remaining: Map<string, number> = new Map();
  for (const n of nodes) remaining.set(n.id, n.capacity);

  const assignmentMap: Map<string, string | null> = new Map();
  for (const r of input.requests) {
    let chosen: string | null = null;
    let chosenCost = Number.POSITIVE_INFINITY;
    for (const n of nodes) {
      const left = remaining.get(n.id) ?? 0;
      if (left < r.demand) continue;
      const cost = n.region === r.region ? 0 : input.costs[r.region][n.region];
      if (cost < chosenCost || (cost === chosenCost && chosen !== null && n.id < chosen)) {
        chosen = n.id;
        chosenCost = cost;
      }
    }
    if (chosen !== null) {
      remaining.set(chosen, (remaining.get(chosen) ?? 0) - r.demand);
    }
    assignmentMap.set(r.id, chosen);
  }

  return assembleResult(
    input,
    (requestId) => assignmentMap.get(requestId) ?? null,
    false,
    { statesExplored: input.requests.length, maxStates: input.requests.length },
  );
}
