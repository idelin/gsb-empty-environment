import { test } from "node:test";
import assert from "node:assert/strict";
import { generateCandidates } from "../src/candidates.ts";
import { diffTokens } from "../src/diff.ts";
import { verifyDiff } from "../src/verify.ts";

test("nested candidates: the larger containing block wins on coverage", () => {
  // "b c" is nested inside the larger match "a b c" at new position 2.
  const oldTokens = ["a", "b", "c"];
  const newTokens = ["b", "c", "a", "b", "c"];
  const candidates = generateCandidates(oldTokens, newTokens);
  const keys = candidates.map((c) => [c.oldStart, c.newStart, c.length]);
  // Both the containing block and the nested fragment are candidates.
  assert.ok(keys.some((k) => k[0] === 0 && k[1] === 2 && k[2] === 3));
  assert.ok(keys.some((k) => k[0] === 1 && k[1] === 0 && k[2] === 2));

  const r = diffTokens(oldTokens, newTokens);
  assert.ok(verifyDiff(oldTokens, newTokens, r).ok);
  assert.equal(r.coveredTokens, 3);
  assert.equal(r.blocks.length, 1);
  assert.deepEqual(
    [r.blocks[0].oldStart, r.blocks[0].newStart, r.blocks[0].length, r.blocks[0].kind],
    [0, 2, 3, "keep"],
  );
  assert.deepEqual(
    r.insertions.map((i) => [i.start, i.length]),
    [[0, 2]],
  );
});

test("equal coverage and count: fewer blocks beats nested fragments", () => {
  // The whole "a b c" can be preserved as one block, or as nested fragments
  // covering the same tokens; the single block must win.
  const oldTokens = ["a", "b", "c"];
  const newTokens = ["a", "b", "c", "a", "b"];
  const r = diffTokens(oldTokens, newTokens);
  assert.ok(verifyDiff(oldTokens, newTokens, r).ok);
  assert.equal(r.coveredTokens, 3);
  assert.equal(r.blocks.length, 1);
  assert.deepEqual([r.blocks[0].oldStart, r.blocks[0].newStart, r.blocks[0].length], [0, 0, 3]);
});

test("full tie on coverage and count is decided by source position", () => {
  // Two symmetric matchings of "a a" exist; the one anchored at the
  // earliest source positions wins.
  const oldTokens = ["a", "x", "a"];
  const newTokens = ["a", "a"];
  const r = diffTokens(oldTokens, newTokens);
  assert.ok(verifyDiff(oldTokens, newTokens, r).ok);
  assert.equal(r.coveredTokens, 2);
  assert.deepEqual(r.newLabels, ["o#0", "o#2"]);
  assert.deepEqual(
    r.deletions.map((d) => [d.start, d.tokens]),
    [[1, ["x"]]],
  );
  assert.ok(r.blocks.every((b) => b.kind === "keep"));
});
