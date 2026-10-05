import { TextEditor, InvalidEncodingError } from "../src/index";
import {
  Model,
  assert,
  assertEq,
  checkRope,
  nodeSet,
  textEncoder,
} from "./helpers";

function expectInvariant(editor: TextEditor, model: Model, ctx: string): void {
  assertEq(editor.getText(), model.text(), ctx + ": text");
  assertEq(editor.scalarLength, model.scalarLength(), ctx + ": scalarLength");
  assertEq(editor.byteLength, model.byteLength(), ctx + ": byteLength");
  assertEq(editor.liveNodes, editor.debugReachableNodes(), ctx + ": liveNodes == reachable");
  const agg = checkRope(editor.debugRoot());
  assertEq(agg.scalars, model.scalarLength(), ctx + ": rope scalar aggregate");
  assertEq(agg.bytes, model.byteLength(), ctx + ": rope byte aggregate");
}

/** Deterministic mixed-width text: ASCII + 2/3/4-byte scalars. */
function sampleText(scalars: number): string {
  const alphabet = ["a", "é", "中", "😀", "b", "🎉", "ᾯ", "𝄞"];
  const parts: string[] = [];
  for (let i = 0; i < scalars; i++) parts.push(alphabet[i % alphabet.length]);
  return parts.join("");
}

export const tests: Array<[string, () => void]> = [
  ["basic insert/delete vs model", () => {
    const e = new TextEditor();
    const m = new Model();
    e.insert(0, "hello");
    m.insert(0, "hello");
    e.insert(5, " 中😀");
    m.insert(5, " 中😀");
    e.insert(0, "é");
    m.insert(0, "é");
    e.delete(1, 3);
    m.delete(1, 3);
    expectInvariant(e, m, "basic");
    assertEq(e.getText(), "élo 中😀", "basic final text");
  }],

  ["cross-leaf deletion", () => {
    const text = sampleText(500); // many leaves (LEAF_MAX = 32)
    const e = new TextEditor(text);
    const m = new Model(text);
    expectInvariant(e, m, "cross-leaf setup");
    // delete a range spanning several leaves, cutting across widths
    e.delete(37, 400);
    m.delete(37, 400);
    expectInvariant(e, m, "cross-leaf delete");
    // delete across the entire remaining content
    e.delete(0, e.scalarLength);
    m.delete(0, m.scalarLength());
    expectInvariant(e, m, "cross-leaf delete-all");
    assertEq(e.getText(), "", "empty after delete-all");
  }],

  ["empty string and empty document", () => {
    const e = new TextEditor();
    const m = new Model();
    assertEq(e.snapshotCount, 1, "initial history size");
    e.insert(0, ""); // no-op
    e.delete(0, 0); // no-op
    assertEq(e.snapshotCount, 1, "no-ops must not create history");
    assertEq(e.getText(), "", "empty text");
    assertEq(e.scalarLength, 0, "empty scalars");
    assertEq(e.byteLength, 0, "empty bytes");
    assertEq(e.getBytes().length, 0, "empty byte array");
    e.insert(0, "x");
    m.insert(0, "x");
    e.delete(0, 1);
    m.delete(0, 1);
    expectInvariant(e, m, "empty after roundtrip");
    assert(!e.undo() === false, "undo available");
    e.undo(); e.undo(); // back to empty
    assertEq(e.getText(), "", "undo to empty");
    assert(!e.undo(), "undo at root returns false");
  }],

  ["four-byte characters are atomic scalars", () => {
    const e = new TextEditor("😀😀😀");
    assertEq(e.scalarLength, 3, "emoji scalars");
    assertEq(e.byteLength, 12, "emoji bytes");
    e.insert(1, "a");
    assertEq(e.getText(), "😀a😀😀", "insert between emoji");
    assertEq(e.byteLength, 13, "bytes after ascii insert");
    e.delete(0, 1); // removes exactly one whole emoji (4 bytes)
    assertEq(e.getText(), "a😀😀", "delete one emoji");
    assertEq(e.byteLength, 9, "bytes after emoji delete");
    // scalar slice never splits a surrogate pair
    assertEq(e.getText(1, 2), "😀", "scalar slice of emoji");
    assertEq(e.getText(0, 1), "a", "scalar slice of ascii");
    // long run of 4-byte chars across leaves
    const many = "😀".repeat(100);
    const e2 = new TextEditor(many);
    const m2 = new Model(many);
    e2.delete(25, 50);
    m2.delete(25, 50);
    expectInvariant(e2, m2, "emoji cross-leaf");
    assertEq(e2.byteLength, 50 * 4, "emoji bytes after delete");
  }],

  ["invalid encoding rejects the whole edit", () => {
    const badBytes: Uint8Array[] = [
      new Uint8Array([0x80]),                   // stray continuation
      new Uint8Array([0xc0, 0x80]),             // overlong NUL
      new Uint8Array([0xc1, 0xbf]),             // overlong
      new Uint8Array([0xe0, 0x80, 0x80]),       // overlong 3-byte
      new Uint8Array([0xed, 0xa0, 0x80]),       // surrogate U+D800
      new Uint8Array([0xed, 0xbf, 0xbf]),       // surrogate U+DFFF
      new Uint8Array([0xf0, 0x80, 0x80, 0x80]), // overlong 4-byte
      new Uint8Array([0xf4, 0x90, 0x80, 0x80]), // > U+10FFFF
      new Uint8Array([0xf5, 0x80, 0x80, 0x80]), // out of range lead
      new Uint8Array([0xff]),                   // invalid byte
      new Uint8Array([0xe4, 0xb8]),             // truncated 3-byte
      new Uint8Array([0x61, 0xf0, 0x9f, 0x98]), // ascii + truncated emoji
    ];
    const badStrings = ["\uD800", "\uDC00", "a\uD800b", "\uDFFFx", "😀".slice(0, 1)];
    for (const bad of badBytes) {
      const e = new TextEditor("保持中😀");
      const before = e.getText();
      const snaps = e.snapshotCount;
      const live = e.liveNodes;
      let threw = false;
      try {
        e.insert(1, bad);
      } catch (err) {
        threw = err instanceof InvalidEncodingError;
      }
      assert(threw, "invalid bytes must throw InvalidEncodingError");
      assertEq(e.getText(), before, "document unchanged after rejected edit");
      assertEq(e.snapshotCount, snaps, "no history entry for rejected edit");
      assertEq(e.liveNodes, live, "no node leak after rejected edit");
    }
    for (const bad of badStrings) {
      const e = new TextEditor("abc");
      let threw = false;
      try {
        e.insert(0, bad);
      } catch (err) {
        threw = err instanceof InvalidEncodingError;
      }
      assert(threw, "lone surrogate string must throw: " + JSON.stringify(bad));
      assertEq(e.getText(), "abc", "document unchanged after rejected string");
    }
    // constructor also rejects invalid input
    let ctorThrew = false;
    try {
      new TextEditor(new Uint8Array([0xed, 0xa0, 0x80]));
    } catch (err) {
      ctorThrew = err instanceof InvalidEncodingError;
    }
    assert(ctorThrew, "constructor rejects invalid UTF-8");
    // boundary-valid inputs are accepted
    const ok = new TextEditor();
    ok.insert(0, new Uint8Array([0xf4, 0x8f, 0xbf, 0xbf])); // U+10FFFF
    ok.insert(0, new Uint8Array([0xed, 0x9f, 0xbf]));        // U+D7FF
    ok.insert(0, new Uint8Array([0xee, 0x80, 0x80]));        // U+E000
    assertEq(ok.scalarLength, 3, "boundary code points accepted");
  }],

  ["undo, redo and branching history", () => {
    const e = new TextEditor();
    const m = new Model();
    e.insert(0, "A");
    m.insert(0, "A");
    const s1 = e.snapshot("v1");
    m.snapshot();
    e.insert(1, "B");
    m.insert(1, "B");
    e.insert(2, "C");
    m.insert(2, "C");
    assertEq(e.getText(), "ABC", "linear edits");
    assert(e.undo() && m.undo(), "undo 1");
    assert(e.undo() && m.undo(), "undo 2");
    assertEq(e.getText(), "A", "undone to v1");
    // diverge: edit after undo creates a branch
    e.insert(1, "中😀");
    m.insert(1, "中😀");
    assertEq(e.getText(), "A中😀", "branch edit");
    // redo follows the most recent branch
    assert(!e.redo(), "redo at tip returns false");
    // jump back to the fork and down the other branch
    e.checkout(s1);
    m.checkout(s1);
    assertEq(e.getText(), "A", "checkout fork");
    assert(e.redo() && m.redo(), "redo into newest branch");
    assertEq(e.getText(), m.text(), "redo matches model");
    // the older branch is still reachable by id
    const info = e.snapshotInfo(s1);
    assertEq(info.children.length, 2, "fork has two children");
    e.checkout(info.children[0]);
    m.checkout(info.children[0]);
    assertEq(e.getText(), "AB", "older branch preserved");
    expectInvariant(e, m, "branching");
  }],

  ["old snapshots share unchanged fragments", () => {
    const text = sampleText(2000);
    const e = new TextEditor(text);
    const base = e.snapshot("base");
    const before = nodeSet(e.debugRootOf(base));
    const liveBefore = e.liveNodes;
    // small localized edit: path copying must share almost everything
    e.insert(1000, "插入😀");
    const liveAfterSmall = e.liveNodes;
    const delta = liveAfterSmall - liveBefore;
    assert(delta > 0, "edit allocates some nodes");
    assert(delta <= 64, "small edit shares almost all nodes, delta=" + delta);
    const after = nodeSet(e.debugRoot());
    let shared = 0;
    before.forEach((n) => {
      if (after.has(n)) shared++;
    });
    assert(shared >= before.size - delta, "old snapshot nodes reused in new tree");
    assert(shared > before.size * 0.8, "most of the old tree is shared");
    // old snapshot content is intact
    assertEq(e.debugRootOf(base) !== null, true, "base root alive");
    const e2text = (() => {
      const keep = e.currentSnapshotId;
      e.checkout(base);
      const t = e.getText();
      e.checkout(keep);
      return t;
    })();
    assertEq(e2text, text, "base snapshot text preserved");
  }],

  ["snapshot release reclaims unreachable nodes", () => {
    const e = new TextEditor(sampleText(1000));
    const baseId = e.currentSnapshotId;
    const baseNodes = nodeSet(e.debugRootOf(baseId)).size;
    assertEq(e.liveNodes, baseNodes, "live == tree size initially");
    // build a linear history of 10 full-document replacements
    for (let i = 0; i < 10; i++) {
      e.delete(0, e.scalarLength);
      e.insert(0, Array.from(sampleText(1000)).reverse().join("") + i);
    }
    const liveWithHistory = e.liveNodes;
    assert(liveWithHistory > baseNodes * 5, "history retains old trees");
    // walk back to the initial snapshot, then release every descendant
    e.checkout(baseId);
    let released = 0;
    for (;;) {
      const ids: number[] = [];
      // find a releasable leaf (not current, no children)
      for (let id = 0; id <= baseId + 20; id++) {
        try {
          const info = e.snapshotInfo(id);
          if (id !== e.currentSnapshotId && info.children.length === 0) ids.push(id);
        } catch (_) {
          // already released
        }
      }
      if (ids.length === 0) break;
      for (const id of ids) {
        e.releaseSnapshot(id);
        released++;
        assertEq(
          e.liveNodes,
          e.debugReachableNodes(),
          "pool consistent after release #" + released
        );
      }
    }
    assert(released >= 10, "released the whole branch, got " + released);
    // only the initial (empty-of-history) tree remains
    assertEq(e.liveNodes, baseNodes, "all descendant nodes reclaimed");
    assertEq(e.snapshotCount, 1, "only current snapshot remains");
    assertEq(e.getText(), sampleText(1000), "current text intact");
  }],

  ["release guards", () => {
    const e = new TextEditor("abc");
    const root = e.snapshot("root");
    e.insert(3, "d");
    const child = e.snapshot("child");
    let threw = false;
    try {
      e.releaseSnapshot(child); // current
    } catch (_) {
      threw = true;
    }
    assert(threw, "cannot release current snapshot");
    e.checkout(root);
    threw = false;
    try {
      e.releaseSnapshot(root); // has a child
    } catch (_) {
      threw = true;
    }
    assert(threw, "cannot release snapshot with descendants");
    threw = false;
    try {
      e.releaseSnapshot(999);
    } catch (_) {
      threw = true;
    }
    assert(threw, "cannot release unknown snapshot");
    e.releaseSnapshot(child); // now a leaf: ok
    assertEq(e.snapshotCount, 1, "child released");
  }],

  ["byte view round-trips through UTF-8", () => {
    const text = sampleText(300);
    const e = new TextEditor(text);
    const bytes = e.getBytes();
    assertEq(bytes.length, e.byteLength, "byte view length");
    const ref = textEncoder.encode(text) as Uint8Array;
    assertEq(bytes.length, ref.length, "matches TextEncoder length");
    for (let i = 0; i < ref.length; i++) {
      assertEq(bytes[i], ref[i], "byte " + i);
    }
    // feed the bytes back in
    const e2 = new TextEditor(bytes);
    assertEq(e2.getText(), text, "bytes decode to same text");
  }],
];
