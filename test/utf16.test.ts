import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { diffPatch } from "../src/diff.ts";
import { applyPatch } from "../src/patch.ts";
import { digestText } from "../src/digest.ts";
import { makePatch, PatchApplyError } from "../src/ops.ts";
import { splitsSurrogatePair, utf16Length } from "../src/utf16.ts";

describe("UTF-16 offsets", () => {
  it("counts astral characters as two UTF-16 code units", () => {
    assert.equal(utf16Length("😀"), 2);
    const base = "a😀b";
    const patch = diffPatch(base, "a😀Xb");
    // retain 3 covers "a😀" (1 + 2 code units).
    assert.deepEqual(
      patch.ops.map((op) => op.kind),
      ["retain", "insert", "retain"],
    );
    assert.equal(applyPatch(base, patch), "a😀Xb");
  });

  it("refuses a delete that splits a surrogate pair", () => {
    const base = "x😀y"; // offsets: x=0, 😀=1..2, y=3
    const split = makePatch(utf16Length(base), digestText(base), [
      { kind: "retain", count: 2 }, // cuts between the surrogate halves
      { kind: "delete", count: 1 },
      { kind: "retain", count: 1 },
    ]);
    assert.throws(() => applyPatch(base, split), PatchApplyError);
  });

  it("refuses an insert positioned inside a surrogate pair", () => {
    const base = "😀";
    const bad = makePatch(utf16Length(base), digestText(base), [
      { kind: "retain", count: 1 },
      { kind: "insert", id: "i1", text: "z" },
      { kind: "retain", count: 1 },
    ]);
    assert.throws(() => applyPatch(base, bad), PatchApplyError);
  });

  it("refuses insert text containing lone surrogates", () => {
    const base = "ab";
    const bad = makePatch(utf16Length(base), digestText(base), [
      { kind: "retain", count: 1 },
      { kind: "insert", id: "i2", text: "\ud800" },
      { kind: "retain", count: 1 },
    ]);
    assert.throws(() => applyPatch(base, bad), PatchApplyError);
  });

  it("diffPatch never emits a boundary that splits a surrogate pair", () => {
    const bases = ["😀", "a😀", "😀a", "😀😀😀", "x😀y😀z"];
    const editeds = ["😀", "😀😀", "a😀b", "😀x", ""];
    for (const base of bases) {
      for (const edited of editeds) {
        const patch = diffPatch(base, edited);
        // Walk the patch and check every boundary in the base text.
        let pos = 0;
        for (const op of patch.ops) {
          assert.ok(!splitsSurrogatePair(base, pos), `${base} -> ${edited}`);
          if (op.kind === "retain" || op.kind === "delete") pos += op.count;
        }
        assert.equal(applyPatch(base, patch), edited);
      }
    }
  });

  it("handles LF and CRLF newlines distinctly and correctly", () => {
    const lf = "a\nb\nc\n";
    const crlf = "a\r\nb\r\nc\r\n";
    const toCrlf = diffPatch(lf, crlf);
    assert.equal(applyPatch(lf, toCrlf), crlf);
    const toLf = diffPatch(crlf, lf);
    assert.equal(applyPatch(crlf, toLf), lf);
    // CRLF is two code units; offsets must reflect that.
    const insertAfterFirstLine = diffPatch(crlf, "a\r\nNEW\r\nb\r\nc\r\n");
    assert.equal(applyPatch(crlf, insertAfterFirstLine), "a\r\nNEW\r\nb\r\nc\r\n");
  });
});
