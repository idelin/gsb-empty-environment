import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { createIdGen } from "../src/ids.ts";
import { diffPatch } from "../src/diff.ts";
import { applyPatch, invertPatch } from "../src/patch.ts";
import {
  applySplice,
  randomSplice,
  randomText,
  referenceApply,
  rng,
  spliceOps,
} from "./helpers.ts";
import { digestText } from "../src/digest.ts";
import { makePatch } from "../src/ops.ts";
import { utf16Length } from "../src/utf16.ts";

const CASES: [string, string][] = [
  ["hello world", "hello brave new world"],
  ["", "from nothing"],
  ["to nothing", ""],
  ["same", "same"],
  ["emoji 😀 middle 😀 end", "emoji 😀 edited 😀 end"],
  ["😀😀😀", "😀x😀"],
  ["a\nb\nc", "a\r\nb\r\nc"],
  ["line1\r\nline2\r\n", "line1\nline2\nline3\n"],
  ["foo foo foo foo", "foo bar foo foo"],
  ["aaaaaa", "aaa"],
  ["mixed 😀\r\nfoo 😀\nfoo", "😀 foo\nfoo\r\n😀"],
];

describe("apply + invert", () => {
  it("apply(diffPatch(base, edited)) === edited", () => {
    for (const [base, edited] of CASES) {
      const patch = diffPatch(base, edited);
      assert.equal(applyPatch(base, patch), edited);
    }
  });

  it("P then inverse(P) restores the byte-exact original", () => {
    for (const [base, edited] of CASES) {
      const patch = diffPatch(base, edited);
      const inverse = invertPatch(base, patch);
      const restored = applyPatch(edited, inverse);
      assert.equal(restored, base);
      assert.deepEqual(
        Buffer.from(restored, "utf8"),
        Buffer.from(base, "utf8"),
        "restored bytes must equal original bytes",
      );
    }
  });

  it("inverse of the inverse reapplies the edit", () => {
    for (const [base, edited] of CASES) {
      const patch = diffPatch(base, edited);
      const inverse = invertPatch(base, patch);
      const forward = invertPatch(edited, inverse);
      assert.equal(applyPatch(base, forward), edited);
    }
  });

  it("matches the stepwise reference on randomized splices", () => {
    const rand = rng(42);
    const ids = createIdGen("t");
    for (let iter = 0; iter < 300; iter++) {
      const base = randomText(rand);
      const splice = randomSplice(rand, base);
      const patch = makePatch(
        utf16Length(base),
        digestText(base),
        spliceOps(base, splice, ids()),
      );
      const expected = applySplice(base, splice);
      assert.equal(applyPatch(base, patch), expected, `iter ${iter} apply`);
      assert.equal(referenceApply(base, patch), expected, `iter ${iter} reference`);
      const inverse = invertPatch(base, patch, ids);
      assert.equal(applyPatch(expected, inverse), base, `iter ${iter} invert`);
      assert.equal(referenceApply(expected, inverse), base, `iter ${iter} invert ref`);
    }
  });
});
