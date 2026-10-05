import { test } from "node:test";
import assert from "node:assert/strict";
import { generateCandidates } from "../src/candidates.ts";
import { diffTokens } from "../src/diff.ts";
import { selectBlocks } from "../src/select.ts";
import type { Block } from "../src/types.ts";
import { verifyDiff } from "../src/verify.ts";
import { mulberry32, randomTokens } from "./helpers.ts";

test("budget exhaustion still returns a verifiable solution", () => {
  const rand = mulberry32(7);
  const oldTokens = randomTokens(rand, 10, 3);
  const newTokens = randomTokens(rand, 10, 3);

  const limited = diffTokens(oldTokens, newTokens, { maxStates: 1 });
  assert.equal(limited.optimal, false);
  const verification = verifyDiff(oldTokens, newTokens, limited);
  assert.ok(verification.ok, verification.errors.join("; "));

  const full = diffTokens(oldTokens, newTokens, { maxStates: 5_000_000 });
  assert.ok(full.optimal);
  assert.ok(full.coveredTokens >= limited.coveredTokens);
});

test("zero budget falls back to the greedy seed", () => {
  const rand = mulberry32(11);
  const oldTokens = randomTokens(rand, 10, 3);
  const newTokens = randomTokens(rand, 10, 3);
  const candidates = generateCandidates(oldTokens, newTokens);

  const selection = selectBlocks(candidates, { maxStates: 0 });
  assert.equal(selection.optimal, false);
  assert.equal(selection.statesExplored, 1);

  // The greedy seed takes longest compatible blocks first.
  const byLength = [...candidates].sort(
    (a, b) => b.length - a.length || a.oldStart - b.oldStart || a.newStart - b.newStart,
  );
  const seed: Block[] = [];
  for (const c of byLength) {
    const compatible = seed.every(
      (s) =>
        (s.oldStart + s.length <= c.oldStart || c.oldStart + c.length <= s.oldStart) &&
        (s.newStart + s.length <= c.newStart || c.newStart + c.length <= s.newStart),
    );
    if (compatible) seed.push(c);
  }
  assert.equal(selection.covered, seed.reduce((sum, b) => sum + b.length, 0));
});

test("generous budget proves optimality", () => {
  const r = diffTokens(["a", "b", "c"], ["b", "c", "a"], { maxStates: 1000 });
  assert.ok(r.optimal);
  assert.equal(r.coveredTokens, 3);
});
