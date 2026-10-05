import { InvalidInputError } from "./types.ts";
import type { BlockedEvent, Problem, SolveResult } from "./types.ts";

export const MAX_EVENTS = 22;

const ABSENT = 0;
const PRESENT = 1;
const DELETED = 2;

const CREATE = 0;
const UPDATE = 1;
const DELETE = 2;

const OP_CODES: Record<string, number> = {
  create: CREATE,
  update: UPDATE,
  delete: DELETE,
};

function popcount(x: number): number {
  let count = 0;
  while (x !== 0) {
    x &= x - 1;
    count += 1;
  }
  return count;
}

/**
 * Finds the lexicographically smallest total order of event ids that satisfies
 * both the explicit precedence pairs and the per-object create/update/delete
 * state machine:
 *
 *   - an object with a `create` event starts ABSENT, otherwise it pre-exists;
 *   - `create` requires ABSENT  -> becomes PRESENT;
 *   - `update` requires PRESENT -> stays PRESENT;
 *   - `delete` requires PRESENT -> becomes DELETED, no event may follow.
 *
 * The two constraint families are checked jointly at every step (no separate
 * "topological sort first, state check later" phase): a greedy lexicographic
 * walk only emits an event when an exact, memoized feasibility search proves
 * the remainder can still be completed under both constraint kinds.
 *
 * When no complete order exists, the result pinpoints the longest locally
 * valid greedy prefix plus the precedence/state constraints blocking it.
 */
export function solve(problem: Problem): SolveResult {
  if (problem === null || typeof problem !== "object" || !Array.isArray(problem.events)) {
    throw new InvalidInputError('input must be an object with an "events" array');
  }
  const events = problem.events;
  if (events.length > MAX_EVENTS) {
    throw new InvalidInputError(`too many events: ${events.length} (maximum is ${MAX_EVENTS})`);
  }
  const ids = new Set<string>();
  for (const event of events) {
    if (event === null || typeof event !== "object") {
      throw new InvalidInputError("every event must be an object");
    }
    if (typeof event.id !== "string" || event.id.length === 0) {
      throw new InvalidInputError('every event needs a non-empty string "id"');
    }
    if (ids.has(event.id)) {
      throw new InvalidInputError(`duplicate event id "${event.id}"`);
    }
    ids.add(event.id);
    if (typeof event.object !== "string" || event.object.length === 0) {
      throw new InvalidInputError(`event "${event.id}" needs a non-empty string "object"`);
    }
    if (typeof event.op !== "string" || !(event.op in OP_CODES)) {
      throw new InvalidInputError(
        `event "${event.id}" has unknown op ${JSON.stringify(event.op)} (expected create|update|delete)`,
      );
    }
  }
  const precedence = problem.precedence ?? [];
  if (!Array.isArray(precedence)) {
    throw new InvalidInputError('"precedence" must be an array of [before, after] pairs');
  }
  for (const pair of precedence) {
    if (!Array.isArray(pair) || pair.length !== 2 || !ids.has(pair[0]) || !ids.has(pair[1])) {
      throw new InvalidInputError(
        `invalid precedence pair ${JSON.stringify(pair)}: expected two known event ids`,
      );
    }
  }

  const n = events.length;
  if (n === 0) {
    return { ok: true, order: [] };
  }

  // Bit index == rank of the event id, so scanning bits 0..n-1 visits ids in
  // ascending lexicographic order.
  const sorted = [...events].sort((a, b) => (a.id < b.id ? -1 : 1));
  const idOf = sorted.map((event) => event.id);
  const indexOf = new Map<string, number>();
  idOf.forEach((id, index) => indexOf.set(id, index));

  const objectIndex = new Map<string, number>();
  const objectName: string[] = [];
  const objOf: number[] = new Array<number>(n);
  const opOf: number[] = new Array<number>(n);
  sorted.forEach((event, index) => {
    let oi = objectIndex.get(event.object);
    if (oi === undefined) {
      oi = objectName.length;
      objectIndex.set(event.object, oi);
      objectName.push(event.object);
    }
    objOf[index] = oi;
    opOf[index] = OP_CODES[event.op];
  });

  const numObjects = objectName.length;
  const initialStates = new Int8Array(numObjects).fill(PRESENT);
  const createEventOf = new Array<number>(numObjects).fill(-1);
  sorted.forEach((event, index) => {
    if (opOf[index] === CREATE) {
      const oi = objOf[index];
      initialStates[oi] = ABSENT;
      if (createEventOf[oi] < 0) createEventOf[oi] = index;
    }
  });

  const predMask = new Array<number>(n).fill(0);
  const successors: number[][] = Array.from({ length: n }, () => []);
  for (const pair of precedence) {
    const before = indexOf.get(pair[0])!;
    const after = indexOf.get(pair[1])!;
    const bit = 1 << before;
    if ((predMask[after] & bit) === 0) {
      predMask[after] |= bit;
      successors[before].push(after);
    }
  }

  const states = Int8Array.from(initialStates);
  const createdBy = new Array<number>(numObjects).fill(-1);
  const deletedBy = new Array<number>(numObjects).fill(-1);

  function tryApply(index: number): boolean {
    const oi = objOf[index];
    const state = states[oi];
    if (opOf[index] === CREATE) {
      if (state !== ABSENT) return false;
      states[oi] = PRESENT;
      createdBy[oi] = index;
      return true;
    }
    if (opOf[index] === UPDATE) {
      return state === PRESENT;
    }
    if (state !== PRESENT) return false;
    states[oi] = DELETED;
    deletedBy[oi] = index;
    return true;
  }

  function undoApply(index: number): void {
    const oi = objOf[index];
    if (opOf[index] === CREATE) {
      states[oi] = ABSENT;
      createdBy[oi] = -1;
    } else if (opOf[index] === DELETE) {
      states[oi] = PRESENT;
      deletedBy[oi] = -1;
    }
  }

  // Exact feasibility oracle. The object states are a pure function of the
  // emitted event set (== fullMask ^ remaining) along any valid path, so a
  // bitmask memo is sound. 0 = unknown, 1 = infeasible, 2 = feasible.
  const memo = new Uint8Array(1 << n);
  function feasible(remaining: number): boolean {
    if (remaining === 0) return true;
    const cached = memo[remaining];
    if (cached !== 0) return cached === 2;
    let ok = false;
    for (let index = 0; index < n; index += 1) {
      const bit = 1 << index;
      if ((remaining & bit) === 0) continue;
      if ((predMask[index] & remaining) !== 0) continue;
      if (!tryApply(index)) continue;
      ok = feasible(remaining ^ bit);
      undoApply(index);
      if (ok) break;
    }
    memo[remaining] = ok ? 2 : 1;
    return ok;
  }

  const fullMask = (1 << n) - 1;
  let remaining = fullMask;
  const prefix: number[] = [];

  if (feasible(fullMask)) {
    // Greedy lexicographic construction: at each position emit the smallest
    // id that still leaves a feasible completion. The invariant
    // feasible(remaining) guarantees a choice exists at every step.
    while (remaining !== 0) {
      let chosen = -1;
      for (let index = 0; index < n; index += 1) {
        const bit = 1 << index;
        if ((remaining & bit) === 0) continue;
        if ((predMask[index] & remaining) !== 0) continue;
        if (!tryApply(index)) continue;
        if (feasible(remaining ^ bit)) {
          chosen = index;
          break;
        }
        undoApply(index);
      }
      if (chosen < 0) {
        throw new Error("internal error: no feasible successor despite feasible remainder");
      }
      prefix.push(chosen);
      remaining ^= 1 << chosen;
    }
    return { ok: true, order: prefix.map((index) => idOf[index]) };
  }

  // No complete order exists. Emit the longest locally valid prefix (smallest
  // id first) so the report pinpoints exactly where reordering gets stuck.
  states.set(initialStates);
  createdBy.fill(-1);
  deletedBy.fill(-1);
  remaining = fullMask;
  prefix.length = 0;
  while (remaining !== 0) {
    let chosen = -1;
    for (let index = 0; index < n; index += 1) {
      const bit = 1 << index;
      if ((remaining & bit) === 0) continue;
      if ((predMask[index] & remaining) !== 0) continue;
      if (!tryApply(index)) continue;
      chosen = index;
      break;
    }
    if (chosen < 0) break;
    prefix.push(chosen);
    remaining ^= 1 << chosen;
  }

  function idsOfMask(mask: number): string[] {
    const result: string[] = [];
    for (let index = 0; index < n; index += 1) {
      if ((mask & (1 << index)) !== 0) result.push(idOf[index]);
    }
    return result;
  }

  function stateViolation(index: number): string | null {
    const oi = objOf[index];
    const object = objectName[oi];
    const state = states[oi];
    const op = opOf[index];
    if (op === CREATE) {
      if (state === ABSENT) return null;
      if (state === PRESENT) {
        const creator = createdBy[oi] >= 0 ? createdBy[oi] : createEventOf[oi];
        return creator >= 0
          ? `object "${object}" already exists (created by event "${idOf[creator]}")`
          : `object "${object}" already exists`;
      }
      return `object "${object}" was deleted by event "${idOf[deletedBy[oi]]}" and cannot be created again`;
    }
    if (op === UPDATE) {
      if (state === PRESENT) return null;
      if (state === ABSENT) {
        return `object "${object}" does not exist yet (create event "${idOf[createEventOf[oi]]}" has not been emitted)`;
      }
      return `object "${object}" was deleted by event "${idOf[deletedBy[oi]]}"`;
    }
    if (state === PRESENT) return null;
    if (state === ABSENT) {
      return `object "${object}" does not exist yet (create event "${idOf[createEventOf[oi]]}" has not been emitted)`;
    }
    return `object "${object}" was already deleted by event "${idOf[deletedBy[oi]]}"`;
  }

  // Events that cannot be reached even by precedence alone: iteratively peel
  // off remaining events with no unmet predecessors; the rest form (or depend
  // on) a precedence cycle.
  const unmetCount = new Array<number>(n).fill(0);
  const queue: number[] = [];
  for (let index = 0; index < n; index += 1) {
    if ((remaining & (1 << index)) === 0) continue;
    unmetCount[index] = popcount(predMask[index] & remaining);
    if (unmetCount[index] === 0) queue.push(index);
  }
  let removed = 0;
  for (let head = 0; head < queue.length; head += 1) {
    const index = queue[head];
    removed += 1;
    for (const next of successors[index]) {
      if ((remaining & (1 << next)) === 0) continue;
      unmetCount[next] -= 1;
      if (unmetCount[next] === 0) queue.push(next);
    }
  }
  const precedenceCycle: string[] = [];
  if (removed < popcount(remaining)) {
    for (let index = 0; index < n; index += 1) {
      if ((remaining & (1 << index)) !== 0 && unmetCount[index] > 0) {
        precedenceCycle.push(idOf[index]);
      }
    }
  }

  const blocked: BlockedEvent[] = [];
  const precedenceConstraints: Array<[string, string]> = [];
  const stateConstraints: string[] = [];
  for (let index = 0; index < n; index += 1) {
    if ((remaining & (1 << index)) === 0) continue;
    const unmetPredecessors = idsOfMask(predMask[index] & remaining);
    const violation = stateViolation(index);
    blocked.push({ event: idOf[index], unmetPredecessors, stateViolation: violation });
    for (const pred of unmetPredecessors) {
      precedenceConstraints.push([pred, idOf[index]]);
    }
    if (violation !== null && unmetPredecessors.length === 0) {
      stateConstraints.push(`event "${idOf[index]}": ${violation}`);
    }
  }

  return {
    ok: false,
    blockingPrefix: prefix.map((index) => idOf[index]),
    remainingEvents: idsOfMask(remaining),
    blocked,
    precedenceCycle,
    constraints: { precedence: precedenceConstraints, state: stateConstraints },
  };
}
