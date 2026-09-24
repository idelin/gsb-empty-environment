import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { diffPatch } from "../src/diff.ts";
import { createIdGen } from "../src/ids.ts";
import { applyPatch, invertPatch } from "../src/patch.ts";
import { digestText } from "../src/digest.ts";
import { makePatch, normalizeOps } from "../src/ops.ts";
import { utf16Length } from "../src/utf16.ts";

describe("operation identity", () => {
  it("two inserts of identical text get distinct identities", () => {
    const ids = createIdGen("id");
    const p1 = diffPatch("", "hello", ids);
    const p2 = diffPatch("", "hello", ids);
    const i1 = p1.ops.find((op) => op.kind === "insert");
    const i2 = p2.ops.find((op) => op.kind === "insert");
    assert.ok(i1 && i1.kind === "insert");
    assert.ok(i2 && i2.kind === "insert");
    assert.equal(i1.text, i2.text);
    assert.notEqual(i1.id, i2.id);
  });

  it("normalization never merges inserts that share text", () => {
    const ops = normalizeOps([
      { kind: "insert", id: "a", text: "same" },
      { kind: "insert", id: "b", text: "same" },
    ]);
    assert.equal(ops.length, 2);
  });

  it("deletes the intended occurrence in repeated text, not a lookalike", () => {
    const base = "foo foo foo";
    // Delete the middle "foo " (UTF-16 offsets 4..7).
    const patch = makePatch(utf16Length(base), digestText(base), [
      { kind: "retain", count: 4 },
      { kind: "delete", count: 4 },
      { kind: "retain", count: 3 },
    ]);
    assert.equal(applyPatch(base, patch), "foo foo");
    const inverse = invertPatch(base, patch, createIdGen("rev"));
    assert.equal(applyPatch("foo foo", inverse), base);
  });

  it("re-inserted content from inversion carries a fresh identity", () => {
    const base = "abc";
    const patch = diffPatch(base, "");
    const inverse = invertPatch(base, patch, createIdGen("fresh"));
    const reinserted = inverse.ops.find((op) => op.kind === "insert");
    assert.ok(reinserted && reinserted.kind === "insert");
    assert.match(reinserted.id, /^fresh_/);
    assert.equal(reinserted.text, "abc");
  });

  it("identical inserted strings at different positions stay distinguishable", () => {
    const base = "a-b";
    const ids = createIdGen("pos");
    // Insert "XX" after 'a' and (separately) at the very end.
    const step1 = diffPatch(base, "aXX-b", ids);
    const mid = applyPatch(base, step1);
    const step2 = diffPatch(mid, "aXX-bXX", ids);
    const inserts1 = step1.ops.filter((op) => op.kind === "insert");
    const inserts2 = step2.ops.filter((op) => op.kind === "insert");
    assert.equal(inserts1.length, 1);
    assert.equal(inserts2.length, 1);
    assert.ok(inserts1[0].kind === "insert" && inserts2[0].kind === "insert");
    if (inserts1[0].kind === "insert" && inserts2[0].kind === "insert") {
      assert.equal(inserts1[0].text, inserts2[0].text);
      assert.notEqual(inserts1[0].id, inserts2[0].id);
    }
    assert.equal(applyPatch(mid, step2), "aXX-bXX");
  });
});
