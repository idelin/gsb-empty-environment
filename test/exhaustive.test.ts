import test from "node:test";
import assert from "node:assert/strict";
import {
  NoFeasiblePlanError,
  planMigration,
  verifyMigration,
  type MigrationProblem,
} from "../src/index.ts";

interface SmallCase {
  firstCandidates: string[];
  secondCandidates: string[];
  audience: "alice" | "bob";
}

const INSTANCES = [
  { id: "1", principal: "alice" },
  { id: "2", principal: "mallory" },
  { id: "3", principal: "bob" },
] as const;

function allNonEmptySubsets(values: readonly string[]): string[][] {
  const result: string[][] = [];
  for (let mask = 1; mask < 1 << values.length; mask += 1) {
    const subset: string[] = [];
    for (let index = 0; index < values.length; index += 1) {
      if ((mask & (1 << index)) !== 0) subset.push(values[index] as string);
    }
    result.push(subset);
  }
  return result;
}

function product<T>(lists: readonly (readonly T[])[]): T[][] {
  if (lists.length === 0) return [[]];
  const [first, ...rest] = lists;
  const tails = product(rest);
  const result: T[][] = [];
  for (const value of first ?? []) {
    for (const tail of tails) result.push([value, ...tail]);
  }
  return result;
}

/**
 * Deliberately naive reference implementation for this direct-principal
 * subset of the language. It enumerates every reset set, every compatible
 * frame attribution, and every continuation choice.
 */
function bruteForce(candidate: SmallCase): string[] | null {
  const frames = [
    { id: "f1", candidates: candidate.firstCandidates },
    { id: "f2", candidates: candidate.secondCandidates },
  ];
  const ids = INSTANCES.map((entry) => entry.id);
  const authorized = (instanceId: string): boolean => {
    const instance = INSTANCES.find((entry) => entry.id === instanceId);
    return instance?.principal === candidate.audience;
  };

  const resetSubsets: string[][] = [];
  for (let mask = 0; mask < 1 << ids.length; mask += 1) {
    const reset: string[] = [];
    for (let index = 0; index < ids.length; index += 1) {
      if ((mask & (1 << index)) !== 0) reset.push(ids[index] as string);
    }
    resetSubsets.push(reset);
  }
  resetSubsets.sort((left, right) => {
    if (left.length !== right.length) return left.length - right.length;
    return left.join(",") < right.join(",") ? -1 : 1;
  });

  for (const reset of resetSubsets) {
    const resetSet = new Set(reset);
    const remaining = frames.map((frame) => frame.candidates.filter((id) => !resetSet.has(id)));
    if (remaining.some((domain) => domain.length === 0)) continue;

    const histories = product(remaining);
    const mappings = product(remaining);
    let valid = true;
    for (const history of histories) {
      for (const mapping of mappings) {
        for (const choice of [history, mapping]) {
          for (const instanceId of choice) {
            if (!authorized(instanceId)) {
              valid = false;
              break;
            }
          }
          if (!valid) break;
        }
        if (!valid) break;
      }
      if (!valid) break;
    }
    if (valid) return reset;
  }
  return null;
}

test("planner agrees with exhaustive reset, attribution, and mapping enumeration", () => {
  const subsets = allNonEmptySubsets(["1", "2", "3"]);
  let cases = 0;
  for (const firstCandidates of subsets) {
    for (const secondCandidates of subsets) {
      for (const audience of ["alice", "bob"] as const) {
        cases += 1;
        const small: SmallCase = { firstCandidates, secondCandidates, audience };
        const expected = bruteForce(small);
        const problem: MigrationProblem = {
          instances: [...INSTANCES],
          frames: [
            { id: "f1", channels: ["q1"], candidates: firstCandidates },
            { id: "f2", channels: ["q2"], candidates: secondCandidates },
          ],
          messages: [
            {
              id: "m1",
              frame: "f1",
              channel: "q1",
              visibility: "private",
              audience: [audience],
            },
            {
              id: "m2",
              frame: "f2",
              channel: "q2",
              visibility: "private",
              audience: [audience],
            },
          ],
        };

        if (expected === null) {
          assert.throws(
            () => planMigration(problem),
            NoFeasiblePlanError,
            `case ${JSON.stringify(small)} should have no safe plan`,
          );
          continue;
        }

        const plan = planMigration(problem);
        assert.deepEqual(
          plan.reauthenticate,
          expected,
          `case ${JSON.stringify(small)}`,
        );
        const verified = verifyMigration(problem, plan);
        assert.equal(verified.ok, true, `case ${JSON.stringify(small)}`);
      }
    }
  }
  assert.equal(cases, 98);
});
