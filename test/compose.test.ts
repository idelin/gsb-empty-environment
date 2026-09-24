import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { diffPatch } from "../src/diff.ts";
import { createIdGen } from "../src/ids.ts";
import { applyPatch, composePatches, invertPatch } from "../src/patch.ts";
import {
  applySplice,
  randomSplice,
  randomText,
  referenceApply,
  referenceApplyChain,
  rng,
  spliceOps,
} from "./helpers.ts";
import { digestText } from "../src/digest.ts";
import { makePatch, type Patch } from "../src/ops.ts";
import { utf16Length } from "../src/utf16.ts";

function chainPatches(base: string, edits: string[], ids = createIdGen("c")): Patch[] {
  const patches: Patch[] = [];
  let text = base;
  for (const next of edits) {
    const p = diffPatch(text, next, ids);
    patches.push(p);
    text = next;
  }
  return patches;
}

describe("compose", () => {
  it("compose(P, Q) applied once equals P then Q applied sequentially", () => {
    const base = "the quick brown fox\njumps over\nthe lazy dog";
    const edits = [
      "the quick red fox\njumps over\nthe lazy dog",
      "the quick red fox\nleaps over\nthe lazy dog",
      "a quick red fox\nleaps over\nthe lazy cat",
    ];
    const patches = chainPatches(base, edits);
    let composed = patches[0];
    let text = base;
    for (const p of patches) {
      text = applyPatch(text, p);
      if (p !== patches[0]) composed = composePatches(composed, p);
    }
    assert.equal(applyPatch(base, composed), text);
    assert.equal(text, edits[edits.length - 1]);
  });

  it("eliminates adjacent inverse ops: compose(P, inverse(P)) is a pure retain", () => {
    const base = "foo 😀 foo\r\nbar foo";
    const edited = "foo 😀 quux\r\nbar";
    const ids = createIdGen("inv");
    const p = diffPatch(base, edited, ids);
    const inv = invertPatch(base, p, ids);
    const composed = composePatches(p, inv);
    assert.ok(composed.ops.every((op) => op.kind === "retain"));
    const total = composed.ops.reduce((n, op) => n + (op.kind === "retain" ? op.count : 0), 0);
    assert.equal(total, utf16Length(base));
    assert.equal(applyPatch(base, composed), base);
  });

  it("keeps later operations' coordinates anchored when cancellation happens mid-stream", () => {
    // p1 inserts "XX" at 0; p2 deletes those 2 chars and edits the tail;
    // p3 edits further. The cancelled insert must not shift p3's coordinates.
    const base = "alpha beta gamma";
    const ids = createIdGen("mid");
    const after1 = "XX" + base;
    const p1 = diffPatch(base, after1, ids);
    const after2 = "alpha beta GAMMA";
    const p2 = diffPatch(after1, after2, ids);
    const after3 = "alpha beta GAMMA delta";
    const p3 = diffPatch(after2, after3, ids);

    const sequential = applyPatch(applyPatch(applyPatch(base, p1), p2), p3);
    const leftFold = composePatches(composePatches(p1, p2), p3);
    const rightFold = composePatches(p1, composePatches(p2, p3));
    assert.equal(applyPatch(base, leftFold), sequential);
    assert.equal(applyPatch(base, rightFold), sequential);
    assert.equal(sequential, after3);
    // The cancelled insert/delete pair is gone from the composed patch.
    assert.ok(!leftFold.ops.some((op) => op.kind === "insert" && op.text === "XX"));
  });

  it("matches the stepwise reference on random chains (repeated text + newlines)", () => {
    const rand = rng(1337);
    const ids = createIdGen("fz");
    for (let iter = 0; iter < 200; iter++) {
      const base = randomText(rand);
      const patches: Patch[] = [];
      let text = base;
      const steps = 1 + Math.floor(rand() * 4);
      for (let s = 0; s < steps; s++) {
        const splice = randomSplice(rand, text);
        const p = makePatch(utf16Length(text), digestText(text), spliceOps(text, splice, ids()));
        patches.push(p);
        text = applySplice(text, splice);
      }
      const composed = patches.reduce((acc, p) => composePatches(acc, p));
      const expected = referenceApplyChain(base, patches);
      assert.equal(applyPatch(base, composed), expected, `iter ${iter} composed`);
      assert.equal(referenceApply(base, composed), expected, `iter ${iter} composed ref`);
    }
  });

  it("round-trips through compose with inversion on random chains", () => {
    const rand = rng(777);
    const ids = createIdGen("rt");
    for (let iter = 0; iter < 100; iter++) {
      const base = randomText(rand);
      const splice = randomSplice(rand, base);
      const p = makePatch(utf16Length(base), digestText(base), spliceOps(base, splice, ids()));
      const edited = applySplice(base, splice);
      const inv = invertPatch(base, p, ids);
      const there = composePatches(p, inv);
      const back = composePatches(inv, invertPatch(edited, inv, ids));
      assert.equal(applyPatch(base, there), base, `iter ${iter} there`);
      assert.equal(applyPatch(edited, back), edited, `iter ${iter} back`);
    }
  });
});
