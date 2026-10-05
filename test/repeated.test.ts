import { test } from "node:test";
import assert from "node:assert/strict";
import { diffTokens } from "../src/diff.ts";
import { verifyDiff } from "../src/verify.ts";

test("repeated blocks get stable, deterministic provenance labels", () => {
  // The trailing "a b" is moved to the front; "a b x" stays as the backbone.
  const oldTokens = ["a", "b", "x", "a", "b"];
  const newTokens = ["a", "b", "a", "b", "x"];
  const r = diffTokens(oldTokens, newTokens);
  assert.ok(verifyDiff(oldTokens, newTokens, r).ok);
  assert.ok(r.optimal);

  assert.equal(r.coveredTokens, 5);
  assert.equal(r.blocks.length, 2);
  const moves = r.blocks.filter((b) => b.kind === "move");
  assert.equal(moves.length, 1);
  assert.deepEqual([moves[0].oldStart, moves[0].newStart, moves[0].length], [3, 0, 2]);

  // Every new token is traced to a concrete old occurrence.
  assert.deepEqual(r.newLabels, ["o#3", "o#4", "o#0", "o#1", "o#2"]);
  assert.deepEqual(r.oldLabels, ["o#0", "o#1", "o#2", "o#3", "o#4"]);
  assert.equal(r.deletions.length, 0);
  assert.equal(r.insertions.length, 0);

  // Determinism: identical input yields identical labels.
  const again = diffTokens(oldTokens, newTokens);
  assert.deepEqual(again.newLabels, r.newLabels);
  assert.deepEqual(again.blocks, r.blocks);
});

test("duplicate moved content prefers the earliest source position", () => {
  // "a b" appears twice in the source; the tie is broken by source position,
  // so the first occurrence is the one treated as preserved.
  const oldTokens = ["a", "b", "a", "b"];
  const newTokens = ["a", "b"];
  const r = diffTokens(oldTokens, newTokens);
  assert.ok(verifyDiff(oldTokens, newTokens, r).ok);
  assert.equal(r.coveredTokens, 2);
  assert.equal(r.blocks.length, 1);
  assert.deepEqual([r.blocks[0].oldStart, r.blocks[0].newStart, r.blocks[0].length], [0, 0, 2]);
  assert.deepEqual(r.newLabels, ["o#0", "o#1"]);
  assert.deepEqual(
    r.deletions.map((d) => [d.start, d.length]),
    [[2, 2]],
  );
});
