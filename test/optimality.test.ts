import { test } from "node:test";
import assert from "node:assert/strict";
import { generateCandidates } from "../src/candidates.ts";
import { diffTokens } from "../src/diff.ts";
import { bruteForceBestCoverage, exhaustiveSelect } from "../src/exhaustive.ts";
import { selectBlocks } from "../src/select.ts";
import { verifyDiff } from "../src/verify.ts";
import { mulberry32, randomTokens } from "./helpers.ts";

test("branch-and-bound matches exhaustive subset search on random inputs", () => {
  const rand = mulberry32(42);
  let checked = 0;
  for (let trial = 0; trial < 20000 && checked < 150; trial++) {
    const oldTokens = randomTokens(rand, Math.floor(rand() * 9), 2 + Math.floor(rand() * 3));
    const newTokens = randomTokens(rand, Math.floor(rand() * 9), 2 + Math.floor(rand() * 3));
    const candidates = generateCandidates(oldTokens, newTokens);
    if (candidates.length > 18) continue;

    const selection = selectBlocks(candidates, { maxStates: 1_000_000 });
    assert.ok(selection.optimal, "search should prove optimality with a generous budget");
    const expected = exhaustiveSelect(candidates);
    assert.deepEqual(selection.blocks, expected);

    const result = diffTokens(oldTokens, newTokens);
    const verification = verifyDiff(oldTokens, newTokens, result);
    assert.ok(verification.ok, verification.errors.join("; "));
    checked++;
  }
  assert.ok(checked >= 80, `only ${checked} comparable cases`);
});

test("candidate generation preserves optimal coverage on all tiny inputs", () => {
  const sequences: string[][] = [[]];
  for (let length = 1; length <= 4; length++) {
    for (let mask = 0; mask < 1 << length; mask++) {
      sequences.push(
        Array.from({ length }, (_, i) => (((mask >> i) & 1) === 1 ? "b" : "a")),
      );
    }
  }
  for (const oldTokens of sequences) {
    for (const newTokens of sequences) {
      const bestPossible = bruteForceBestCoverage(oldTokens, newTokens);
      for (const maxCandidates of [undefined, 0]) {
        // maxCandidates 0 forces the reduced fallback (maximal + singletons);
        // both modes must preserve optimal coverage.
        const candidates = generateCandidates(oldTokens, newTokens, { maxCandidates });
        const selection = selectBlocks(candidates, {
          maxStates: 1_000_000,
          oldLength: oldTokens.length,
          newLength: newTokens.length,
        });
        assert.ok(selection.optimal);
        assert.equal(
          selection.covered,
          bestPossible,
          `coverage mismatch for old=${JSON.stringify(oldTokens)} new=${JSON.stringify(newTokens)} maxCandidates=${maxCandidates}`,
        );
      }
    }
  }
});
