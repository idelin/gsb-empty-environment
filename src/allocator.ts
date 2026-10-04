import type { AllocationInput, BudgetOptions, SolveResult } from './types.js';
import { inputError, validateInput } from './validate.js';
import { assembleResult } from './assemble.js';
import { greedyAllocate } from './greedy.js';

export const DEFAULT_MAX_STATES = 1000000;

interface Key {
  admitted: number;
  cost: number;
  migrations: number;
  vector: string[];
}

function compareId(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

function compareVector(a: string[], b: string[]): number {
  for (let i = 0; i < a.length; i++) {
    const x = a[i];
    const y = b[i];
    if (x !== y) return x < y ? -1 : 1;
  }
  return 0;
}

function compareKey(a: Key, b: Key): number {
  if (a.admitted !== b.admitted) return b.admitted - a.admitted;
  if (a.cost !== b.cost) return a.cost - b.cost;
  if (a.migrations !== b.migrations) return a.migrations - b.migrations;
  return compareVector(a.vector, b.vector);
}

export function solveAllocation(input: AllocationInput, budget: BudgetOptions = {}): SolveResult {
  validateInput(input);
  const maxStates = budget.maxStates ?? DEFAULT_MAX_STATES;
  const timeMs = budget.timeMs ?? Number.POSITIVE_INFINITY;
  if (!Number.isInteger(maxStates) || maxStates < 1) {
    throw inputError('budget.maxStates must be a positive integer');
  }
  if (!(timeMs > 0)) throw inputError('budget.timeMs must be a positive number');

  const previous: Record<string, string | null> = input.previous ?? {};
  const nodes = [...input.nodes].sort((a, b) => compareId(a.id, b.id));
  const canon = [...input.requests].sort(
    (a, b) => b.demand - a.demand || compareId(a.id, b.id),
  );
  const requestCount = canon.length;
  const nodeCount = nodes.length;

  const idSortedPos: number[] = new Array(requestCount).fill(0);
  {
    const order: number[] = canon.map((unused, index) => index);
    order.sort((x, y) => compareId(canon[x].id, canon[y].id));
    for (let pos = 0; pos < requestCount; pos++) idSortedPos[order[pos]] = pos;
  }

  const costMat: number[][] = [];
  for (let i = 0; i < requestCount; i++) {
    const row: number[] = [];
    for (let j = 0; j < nodeCount; j++) {
      row.push(
        canon[i].region === nodes[j].region
          ? 0
          : input.costs[canon[i].region][nodes[j].region],
      );
    }
    costMat.push(row);
  }

  const prevHas: boolean[] = [];
  const prevNode: Array<string | null> = [];
  for (let i = 0; i < requestCount; i++) {
    prevHas.push(Object.prototype.hasOwnProperty.call(previous, canon[i].id));
    const value = previous[canon[i].id];
    prevNode.push(value === undefined ? null : value);
  }

  const suffixDemand: number[] = new Array(requestCount + 1).fill(0);
  for (let i = requestCount - 1; i >= 0; i--) {
    suffixDemand[i] = suffixDemand[i + 1] + canon[i].demand;
  }

  const remaining = nodes.map((n) => n.capacity);
  const assignment: Array<string | null> = new Array(requestCount).fill(null);

  let best: Key | null = null;
  let bestAssignment: Array<string | null> = [];
  let states = 0;
  let interrupted = false;
  const start = Date.now();

  function buildVector(): string[] {
    const vector: string[] = new Array(requestCount).fill('');
    for (let i = 0; i < requestCount; i++) {
      vector[idSortedPos[i]] = assignment[i] ?? '';
    }
    return vector;
  }

  function dfs(index: number, admitted: number, cost: number, migrations: number): void {
    if (interrupted) return;
    states++;
    if (states > maxStates || ((states & 1023) === 0 && Date.now() - start > timeMs)) {
      interrupted = true;
      return;
    }
    if (best !== null) {
      const potential = admitted + suffixDemand[index];
      if (potential < best.admitted) return;
      if (potential === best.admitted) {
        if (cost > best.cost) return;
        if (cost === best.cost && migrations > best.migrations) return;
      }
    }
    if (index === requestCount) {
      const key: Key = { admitted, cost, migrations, vector: buildVector() };
      if (best === null || compareKey(key, best) < 0) {
        best = key;
        bestAssignment = assignment.slice();
      }
      return;
    }
    const demand = canon[index].demand;
    for (let j = 0; j < nodeCount; j++) {
      if (remaining[j] < demand) continue;
      remaining[j] -= demand;
      assignment[index] = nodes[j].id;
      const moved = prevHas[index] && prevNode[index] !== nodes[j].id ? 1 : 0;
      dfs(index + 1, admitted + demand, cost + costMat[index][j], migrations + moved);
      remaining[j] += demand;
      assignment[index] = null;
      if (interrupted) return;
    }
    const movedByReject = prevHas[index] && prevNode[index] !== null ? 1 : 0;
    dfs(index + 1, admitted, cost, migrations + movedByReject);
  }

  dfs(0, 0, 0, 0);

  if (best === null) {
    const fallback = greedyAllocate(input);
    fallback.provenOptimal = false;
    fallback.stats.statesExplored = states;
    fallback.stats.maxStates = maxStates;
    return fallback;
  }

  const assignmentMap: Map<string, string | null> = new Map();
  for (let i = 0; i < requestCount; i++) assignmentMap.set(canon[i].id, bestAssignment[i]);

  return assembleResult(
    input,
    (requestId) => assignmentMap.get(requestId) ?? null,
    !interrupted,
    { statesExplored: states, maxStates },
  );
}
