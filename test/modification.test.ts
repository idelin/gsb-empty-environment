import { test } from "node:test";
import assert from "node:assert/strict";
import { diffTokens } from "../src/diff.ts";
import { verifyDiff } from "../src/verify.ts";

test("moved block with a small internal edit is still preserved around the edit", () => {
  // "a b c d" is moved after "e f" with c replaced by x.
  const oldTokens = ["a", "b", "c", "d", "e", "f"];
  const newTokens = ["e", "f", "a", "b", "x", "d"];
  const r = diffTokens(oldTokens, newTokens);
  assert.ok(verifyDiff(oldTokens, newTokens, r).ok);
  assert.ok(r.optimal);

  // The edit splits the moved region into two blocks; everything else is preserved.
  assert.equal(r.coveredTokens, 5);
  assert.deepEqual(
    r.blocks.map((b) => [b.oldStart, b.newStart, b.length]),
    [
      [0, 2, 2],
      [3, 5, 1],
      [4, 0, 2],
    ],
  );

  // Only the edited token shows up as delete + insert.
  assert.deepEqual(
    r.deletions.map((d) => [d.start, d.tokens]),
    [[2, ["c"]]],
  );
  assert.deepEqual(
    r.insertions.map((i) => [i.start, i.tokens]),
    [[4, ["x"]]],
  );

  // "e f" jumped to the front, so it is the move; the edited block kept order.
  const moves = r.blocks.filter((b) => b.kind === "move");
  assert.deepEqual(
    moves.map((b) => [b.oldStart, b.newStart, b.length]),
    [[4, 0, 2]],
  );
  assert.deepEqual(r.newLabels, ["o#4", "o#5", "o#0", "o#1", "n#4", "o#3"]);
});

test("single-token edit inside an otherwise stable block", () => {
  const oldTokens = ["p", "a", "b", "c", "d", "q"];
  const newTokens = ["p", "a", "b", "y", "d", "q"];
  const r = diffTokens(oldTokens, newTokens);
  assert.ok(verifyDiff(oldTokens, newTokens, r).ok);
  assert.equal(r.coveredTokens, 5);
  assert.ok(r.blocks.every((b) => b.kind === "keep"));
  assert.deepEqual(
    r.deletions.map((d) => d.tokens),
    [["c"]],
  );
  assert.deepEqual(
    r.insertions.map((i) => i.tokens),
    [["y"]],
  );
});
