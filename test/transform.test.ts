import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { diffPatch } from "../src/diff.ts";
import { createIdGen } from "../src/ids.ts";
import { applyPatch, rebasePatch, transformOps } from "../src/patch.ts";
import { digestText } from "../src/digest.ts";
import { ConflictError, makePatch, type Patch } from "../src/ops.ts";
import { utf16Length } from "../src/utf16.ts";

function patchOn(base: string, ops: Patch["ops"]): Patch {
  return makePatch(utf16Length(base), digestText(base), ops);
}

/** Verify the swap diagram closes: A-then-rebased-B === B-then-rebased-A. */
function assertCommutes(base: string, a: Patch, b: Patch): string {
  const aPrime = rebasePatch(base, a, b, "left");
  const bPrime = rebasePatch(base, b, a, "right");
  const viaA = applyPatch(applyPatch(base, a), bPrime);
  const viaB = applyPatch(applyPatch(base, b), aPrime);
  assert.equal(viaA, viaB);
  return viaA;
}

describe("transform / three-way relocation", () => {
  it("commutes disjoint delete+insert edits (swap diagram)", () => {
    const base = "hello world foo";
    const a = patchOn(base, [
      { kind: "delete", count: 5 },
      { kind: "insert", id: "a1", text: "hi" },
      { kind: "retain", count: 10 },
    ]);
    const b = patchOn(base, [
      { kind: "retain", count: 12 },
      { kind: "delete", count: 3 },
    ]);
    assert.equal(assertCommutes(base, a, b), "hi world ");
  });

  it("commutes disjoint edits around emoji and CRLF", () => {
    const base = "😀 start\r\nmiddle 😀\r\nend";
    const a = diffPatch(base, "😀 START\r\nmiddle 😀\r\nend", createIdGen("a"));
    const b = diffPatch(base, "😀 start\r\nmiddle 😀\r\nEND!", createIdGen("b"));
    const merged = assertCommutes(base, a, b);
    assert.equal(merged, "😀 START\r\nmiddle 😀\r\nEND!");
  });

  it("orders concurrent inserts at the same position deterministically by op id", () => {
    const base = "ab";
    const a = patchOn(base, [
      { kind: "retain", count: 1 },
      { kind: "insert", id: "op_1", text: "X" },
      { kind: "retain", count: 1 },
    ]);
    const b = patchOn(base, [
      { kind: "retain", count: 1 },
      { kind: "insert", id: "op_2", text: "Y" },
      { kind: "retain", count: 1 },
    ]);
    // op_1 < op_2, so X lands before Y no matter which side rebases first.
    assert.equal(assertCommutes(base, a, b), "aXYb");
  });

  it("same-position inserts with equal text are still two distinct edits", () => {
    const base = "ab";
    const ids = createIdGen("dup");
    const a = diffPatch(base, "aXXb", ids);
    const b = diffPatch(base, "aXXb", ids);
    const merged = assertCommutes(base, a, b);
    assert.equal(merged, "aXXXXb"); // both inserts survive; neither is deduped
  });

  it("rebased patch carries the digest of its new base", () => {
    const base = "one two three";
    const a = diffPatch(base, "one TWO three", createIdGen("ra"));
    const b = diffPatch(base, "one two THREE", createIdGen("rb"));
    const aPrime = rebasePatch(base, a, b);
    const newBase = applyPatch(base, b);
    assert.equal(aPrime.baseDigest, digestText(newBase));
    assert.equal(aPrime.baseLength, utf16Length(newBase));
    assert.equal(applyPatch(newBase, aPrime), "one TWO THREE");
  });

  it("returns a conflict for crossing deletions", () => {
    const base = "0123456789";
    const a = patchOn(base, [
      { kind: "retain", count: 2 },
      { kind: "delete", count: 3 }, // deletes 2..5
      { kind: "retain", count: 5 },
    ]);
    const b = patchOn(base, [
      { kind: "retain", count: 4 },
      { kind: "delete", count: 3 }, // deletes 4..7
      { kind: "retain", count: 3 },
    ]);
    assert.throws(() => transformOps(a.ops!, b.ops!), ConflictError);
    assert.throws(() => rebasePatch(base, a, b), ConflictError);
  });

  it("returns a conflict for identical concurrent deletions", () => {
    const base = "abcdef";
    const ops = (): Patch["ops"] => [
      { kind: "retain", count: 1 },
      { kind: "delete", count: 3 },
      { kind: "retain", count: 2 },
    ];
    const a = patchOn(base, ops());
    const b = patchOn(base, ops());
    assert.throws(() => rebasePatch(base, a, b), ConflictError);
  });

  it("allows adjacent (non-overlapping) deletions", () => {
    const base = "abcde";
    const a = patchOn(base, [
      { kind: "delete", count: 2 },
      { kind: "retain", count: 3 },
    ]);
    const b = patchOn(base, [
      { kind: "retain", count: 3 },
      { kind: "delete", count: 2 },
    ]);
    assert.equal(assertCommutes(base, a, b), "c");
  });

  it("three-way relocation: base + two branches merge into one text", () => {
    const base = "line1\nline2\nline3\n";
    const branchA = diffPatch(base, "line1\nline2 changed by A\nline3\n", createIdGen("A"));
    const branchB = diffPatch(base, "line1\nline2\nline3\nline4 from B\n", createIdGen("B"));
    const merged = assertCommutes(base, branchA, branchB);
    assert.equal(merged, "line1\nline2 changed by A\nline3\nline4 from B\n");
  });
});
