import type { AllocationInput, SolveResult } from './types.js';
import { validateInput } from './validate.js';
import { assembleResult } from './assemble.js';

function compareId(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

export function bruteForceAllocate(input: AllocationInput): SolveResult {
  validateInput(input);
  const previous: Record<string, string | null> = input.previous ?? {};
  const requests = [...input.requests].sort((a, b) => compareId(a.id, b.id));
  const nodes = [...input.nodes].sort((a, b) => compareId(a.id, b.id));
  const remaining = nodes.map((n) => n.capacity);
  const choice: string[] = new Array(requests.length).fill('');

  let states = 0;
  let found = false;
  let bestAdmitted = 0;
  let bestCost = 0;
  let bestMigrations = 0;
  let bestVector: string[] = [];

  function vectorIsLess(a: string[], b: string[]): boolean {
    for (let i = 0; i < a.length; i++) {
      if (a[i] === b[i]) continue;
      return a[i] < b[i];
    }
    return false;
  }

  function walk(index: number, admitted: number, cost: number, migrations: number): void {
    states++;
    if (index === requests.length) {
      const vector = choice.slice();
      const better =
        !found ||
        admitted > bestAdmitted ||
        (admitted === bestAdmitted && cost < bestCost) ||
        (admitted === bestAdmitted && cost === bestCost && migrations < bestMigrations) ||
        (admitted === bestAdmitted &&
          cost === bestCost &&
          migrations === bestMigrations &&
          vectorIsLess(vector, bestVector));
      if (better) {
        found = true;
        bestAdmitted = admitted;
        bestCost = cost;
        bestMigrations = migrations;
        bestVector = vector;
      }
      return;
    }
    const r = requests[index];
    const hasPrev = Object.prototype.hasOwnProperty.call(previous, r.id);
    const prev = hasPrev ? previous[r.id] : undefined;
    for (let j = 0; j < nodes.length; j++) {
      if (remaining[j] < r.demand) continue;
      remaining[j] -= r.demand;
      choice[index] = nodes[j].id;
      const placeCost =
        r.region === nodes[j].region ? 0 : input.costs[r.region][nodes[j].region];
      const moved = hasPrev && prev !== nodes[j].id ? 1 : 0;
      walk(index + 1, admitted + r.demand, cost + placeCost, migrations + moved);
      remaining[j] += r.demand;
    }
    choice[index] = '';
    const movedByReject = hasPrev && prev !== null ? 1 : 0;
    walk(index + 1, admitted, cost, migrations + movedByReject);
  }

  walk(0, 0, 0, 0);

  const assignmentMap: Map<string, string | null> = new Map();
  for (let i = 0; i < requests.length; i++) {
    assignmentMap.set(requests[i].id, bestVector[i] === '' ? null : bestVector[i]);
  }
  return assembleResult(
    input,
    (requestId) => assignmentMap.get(requestId) ?? null,
    true,
    { statesExplored: states, maxStates: -1 },
  );
}
