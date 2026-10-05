import { test } from "node:test";
import assert from "node:assert/strict";
import { diffTokens } from "../src/diff.ts";
import { verifyDiff } from "../src/verify.ts";

function checkedDiff(oldTokens: string[], newTokens: string[]) {
  const result = diffTokens(oldTokens, newTokens);
  const verification = verifyDiff(oldTokens, newTokens, result);
  assert.ok(verification.ok, verification.errors.join("; "));
  assert.ok(result.optimal);
  return result;
}

test("unchanged input is a single keep block", () => {
  const r = checkedDiff(["a", "b", "c"], ["a", "b", "c"]);
  assert.equal(r.blocks.length, 1);
  assert.deepEqual(
    r.blocks.map((b) => [b.oldStart, b.newStart, b.length, b.kind]),
    [[0, 0, 3, "keep"]],
  );
  assert.equal(r.coveredTokens, 3);
  assert.equal(r.deletions.length, 0);
  assert.equal(r.insertions.length, 0);
});

test("pure insertion is not reported as a move", () => {
  const r = checkedDiff(["a", "b"], ["a", "x", "b"]);
  assert.equal(r.coveredTokens, 2);
  assert.ok(r.blocks.every((b) => b.kind === "keep"));
  assert.deepEqual(
    r.insertions.map((r2) => [r2.start, r2.length]),
    [[1, 1]],
  );
  assert.equal(r.deletions.length, 0);
  assert.deepEqual(r.newLabels, ["o#0", "n#1", "o#1"]);
});

test("pure deletion is not reported as a move", () => {
  const r = checkedDiff(["a", "x", "b"], ["a", "b"]);
  assert.equal(r.coveredTokens, 2);
  assert.deepEqual(
    r.deletions.map((r2) => [r2.start, r2.length]),
    [[1, 1]],
  );
  assert.equal(r.insertions.length, 0);
});

test("moved block is distinguished from delete+insert", () => {
  const r = checkedDiff(["a", "b", "c"], ["b", "c", "a"]);
  assert.equal(r.coveredTokens, 3);
  assert.equal(r.deletions.length, 0);
  assert.equal(r.insertions.length, 0);
  const moves = r.blocks.filter((b) => b.kind === "move");
  assert.equal(moves.length, 1);
  assert.deepEqual([moves[0].oldStart, moves[0].newStart, moves[0].length], [0, 2, 1]);
  assert.deepEqual(r.newLabels, ["o#1", "o#2", "o#0"]);
});

test("empty inputs", () => {
  const both = checkedDiff([], []);
  assert.equal(both.blocks.length, 0);
  const inserted = checkedDiff([], ["a"]);
  assert.equal(inserted.insertions.length, 1);
  assert.equal(inserted.newLabels[0], "n#0");
  const deleted = checkedDiff(["a"], []);
  assert.equal(deleted.deletions.length, 1);
});
