import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { TextDocument } from "../src/document.ts";
import { diffPatch } from "../src/diff.ts";
import { createIdGen } from "../src/ids.ts";
import { applyPatch } from "../src/patch.ts";
import { digestText } from "../src/digest.ts";
import { makePatch, PatchApplyError } from "../src/ops.ts";
import { utf16Length } from "../src/utf16.ts";

describe("digest validation", () => {
  it("rejects a same-length base with different content", () => {
    const base = "aaaaaaaaaaaaaaaa";
    const edited = "aaaaPREFIXaaaa";
    const patch = diffPatch(base, edited);
    const impostor = "bbbbbbbbbbbbbbbb"; // same UTF-16 length, different bytes
    assert.equal(utf16Length(impostor), patch.baseLength);
    assert.throws(() => applyPatch(impostor, patch), PatchApplyError);
  });

  it("rejects same content with different length", () => {
    const base = "abc";
    const patch = diffPatch(base, "abc!");
    assert.throws(() => applyPatch("abc ", patch), PatchApplyError);
    assert.throws(() => applyPatch("ab", patch), PatchApplyError);
  });

  it("distinguishes LF from CRLF bases of equal visible content", () => {
    const patch = diffPatch("a\nb", "a\nb\nc");
    assert.throws(() => applyPatch("a\r\nb", patch), PatchApplyError);
  });

  it("rejects a forged digest", () => {
    const base = "hello";
    const patch = makePatch(utf16Length(base), digestText(base), [
      { kind: "retain", count: 5 },
    ]);
    const forged = { ...patch, baseDigest: digestText("hello").replace(/^./, "0") };
    assert.throws(() => applyPatch(base, forged), PatchApplyError);
  });
});

describe("failure atomicity", () => {
  it("a failing apply produces no partial result", () => {
    const base = "foo foo foo";
    const patch = diffPatch(base, "foo bar foo");
    let outcome: string | undefined;
    try {
      outcome = applyPatch("foo quux foo", patch);
    } catch (err) {
      assert.ok(err instanceof PatchApplyError);
    }
    assert.equal(outcome, undefined);
  });

  it("TextDocument is untouched after a failed apply", () => {
    const doc = new TextDocument("important\r\ncontent 😀", createIdGen("doc"));
    const good = diffPatch("other text", "other text!");
    assert.throws(() => doc.apply(good), PatchApplyError);
    assert.equal(doc.getText(), "important\r\ncontent 😀");
  });

  it("rejects malformed patches without mutating state", () => {
    const base = "abc";
    const overrun = makePatch(utf16Length(base), digestText(base), [
      { kind: "retain", count: 10 },
    ]);
    const trailing = makePatch(utf16Length(base), digestText(base), [
      { kind: "retain", count: 2 },
    ]);
    const doc = new TextDocument(base);
    assert.throws(() => doc.apply(overrun), PatchApplyError);
    assert.throws(() => doc.apply(trailing), PatchApplyError);
    assert.equal(doc.getText(), base);
  });

  it("undo restores the byte-exact original after a successful apply", () => {
    const original = "line1\r\nline2 😀\r\nline3";
    const doc = new TextDocument(original, createIdGen("undo"));
    const p1 = diffPatch(original, "line1\nline2 😀\nline3", createIdGen("u1"));
    doc.apply(p1);
    assert.notEqual(doc.getText(), original);
    assert.ok(doc.undo());
    assert.deepEqual(Buffer.from(doc.getText(), "utf8"), Buffer.from(original, "utf8"));
    assert.ok(doc.undo() === false);
  });
});
