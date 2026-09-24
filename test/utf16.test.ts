import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { FoldedDocument } from "../src/document.ts";
import {
  isSafeOffset,
  offsetAt,
  positionAt,
  snapOffset,
} from "../src/utf16.ts";

describe("UTF-16 surrogate safety", () => {
  it("treats emoji as two UTF-16 code units", () => {
    const text = "a😀b";
    assert.equal(text.length, 4);
    assert.equal(isSafeOffset(text, 2), false); // between the surrogates
    assert.equal(isSafeOffset(text, 1), true);
    assert.equal(isSafeOffset(text, 3), true);
  });

  it("snaps offsets outward, never into a surrogate pair", () => {
    const text = "a😀b";
    assert.equal(snapOffset(text, 2, "start"), 1);
    assert.equal(snapOffset(text, 2, "end"), 3);
  });

  it("expands fold boundaries so a placeholder never splits a surrogate pair", () => {
    const doc = new FoldedDocument("x😀y😀z");
    // Naive [1, 5) ends inside the second emoji; the end snaps outward.
    const region = doc.addFold(1, 5);
    assert.deepEqual(region, { start: 1, end: 6 });
    assert.equal(doc.visibleText, "x⋯z");
    assert.equal(doc.text, "x😀y😀z"); // original untouched
  });

  it("keeps emoji intact in the projection and mapping", () => {
    const doc = new FoldedDocument("😀 abc 😀");
    doc.addFold(3, 6); // "abc" (offsets are UTF-16: emoji = 2 units)
    assert.equal(doc.visibleText, "😀 ⋯ 😀");
    assert.equal(doc.toOriginalOffset(4), 6); // just past placeholder
    assert.equal(doc.toVisibleOffset(6), 4);
    assert.equal(doc.toOriginalOffset(5), 7); // start of the trailing emoji
  });

  it("never returns a pair-splitting offset from edits", () => {
    const doc = new FoldedDocument("ab😀cd ef");
    doc.addFold(4, 8); // "cd e"
    const visible = doc.visibleText; // "ab😀⋯f" -> placeholder at [4,5)
    assert.equal(visible, "ab😀⋯f");
    // Insert at the placeholder boundary; result must keep the emoji whole.
    doc.applyVisibleEdit(4, 4, "🎉");
    assert.equal(doc.text, "ab😀🎉cd ef");
    assert.ok(isSafeOffset(doc.text, 4));
  });
});

describe("line endings (LF and CRLF)", () => {
  const lf = "one\ntwo\nthree";
  const crlf = "one\r\ntwo\r\nthree";

  it("computes positions for LF documents", () => {
    assert.deepEqual(positionAt(lf, 5), { line: 1, character: 1 });
    assert.equal(offsetAt(lf, { line: 1, character: 1 }), 5);
    assert.equal(offsetAt(lf, { line: 2, character: 0 }), 8);
  });

  it("computes positions for CRLF documents, counting \\r\\n as one break", () => {
    assert.deepEqual(positionAt(crlf, 6), { line: 1, character: 1 });
    assert.equal(offsetAt(crlf, { line: 1, character: 1 }), 6);
    assert.equal(offsetAt(crlf, { line: 2, character: 0 }), 10);
    // Offset inside the \r\n pair maps to the previous line's end.
    assert.deepEqual(positionAt(crlf, 4), { line: 1, character: 0 });
  });

  it("round-trips position/offset for both LF and CRLF", () => {
    for (const text of [lf, crlf]) {
      for (let o = 0; o <= text.length; o++) {
        const pos = positionAt(text, o);
        const back = offsetAt(text, pos);
        assert.ok(Math.abs(back - o) <= 1, `offset ${o} in ${JSON.stringify(text)}`);
      }
    }
  });

  it("projects folds across lines identically for LF and CRLF", () => {
    const mkLf = new FoldedDocument(lf);
    const mkCrlf = new FoldedDocument(crlf);
    // Fold "two\nthree" / "two\r\nthree" (line 1 -> end of document).
    mkLf.addFold(mkLf.originalOffsetAt({ line: 1, character: 0 }), lf.length);
    mkCrlf.addFold(
      mkCrlf.originalOffsetAt({ line: 1, character: 0 }),
      crlf.length,
    );
    assert.equal(mkLf.visibleText, "one\n⋯");
    assert.equal(mkCrlf.visibleText, "one\r\n⋯");
    assert.deepEqual(mkLf.visiblePositionAt(4), { line: 1, character: 0 });
    assert.deepEqual(mkCrlf.visiblePositionAt(5), { line: 1, character: 0 });
  });
});
