import { test } from "node:test";
import assert from "node:assert/strict";
import {
  EditorDocument,
  EditConflictError,
  SurrogatePairError,
  EditOutOfBoundsError,
  NestedTransactionError,
} from "../src/index.ts";
import type { Annotation, Selection } from "../src/index.ts";

function stateOf(doc: EditorDocument) {
  return {
    text: doc.getText(),
    selections: doc.getSelections(),
    annotations: doc.getAnnotations(),
    undoDepth: doc.undoDepth,
  };
}

test("multiple replacements are interpreted in one baseline coordinate system", () => {
  const doc = new EditorDocument("hello world, foo bar");
  doc.transact((tx) => {
    tx.replace(0, 5, "hi"); // grows by -2
    tx.replace(13, 16, "baz"); // "foo" -> "baz", same length
    tx.replace(17, 20, "quux"); // grows by +1
  });
  assert.equal(doc.getText(), "hi world, baz quux");
});

test("length change in a later replacement never shifts earlier ranges", () => {
  const doc = new EditorDocument("0123456789ABCDEFGHIJ");
  doc.transact((tx) => {
    // Later (higher offset) edit shrinks 10 chars to 1.
    tx.replace(10, 20, "x");
    // Earlier edit must still hit exactly [0, 5) of the baseline.
    tx.replace(0, 5, "abcde");
  });
  assert.equal(doc.getText(), "abcde56789x");
});

test("edits applied regardless of staging order (baseline semantics)", () => {
  const a = new EditorDocument("aaa bbb ccc");
  a.transact((tx) => {
    tx.replace(8, 11, "C");
    tx.replace(0, 3, "AAAAA");
  });
  assert.equal(a.getText(), "AAAAA bbb C");

  const b = new EditorDocument("aaa bbb ccc");
  b.transact((tx) => {
    tx.replace(0, 3, "AAAAA");
    tx.replace(8, 11, "C");
  });
  assert.equal(b.getText(), "AAAAA bbb C");
});

test("overlapping replacements fail before any write", () => {
  const doc = new EditorDocument("hello world");
  doc.setSelections([{ anchor: 0, head: 5 }]);
  doc.setAnnotations([{ id: "a1", range: { start: 6, end: 11 } }]);
  const before = stateOf(doc);

  assert.throws(
    () =>
      doc.transact((tx) => {
        tx.replace(2, 7, "X");
        tx.replace(5, 9, "Y"); // overlaps [2, 7)
      }),
    EditConflictError,
  );
  assert.deepEqual(stateOf(doc), before);
});

test("an edit fully containing another is also a conflict", () => {
  const doc = new EditorDocument("hello world");
  assert.throws(
    () =>
      doc.transact((tx) => {
        tx.replace(0, 11, "x");
        tx.replace(3, 5, "y");
      }),
    EditConflictError,
  );
  assert.equal(doc.getText(), "hello world");
  assert.equal(doc.undoDepth, 0);
});

test("touching (adjacent) edits are allowed", () => {
  const doc = new EditorDocument("aaabbb");
  doc.transact((tx) => {
    tx.replace(0, 3, "A");
    tx.replace(3, 6, "B");
  });
  assert.equal(doc.getText(), "AB");
});

test("two inserts at the same position apply in call order", () => {
  const doc = new EditorDocument("ac");
  doc.transact((tx) => {
    tx.insert(1, "1");
    tx.insert(1, "2");
  });
  assert.equal(doc.getText(), "a12c");
});

test("rejects edit boundaries that split a surrogate pair", () => {
  // "a😀b": a=0, high=1, low=2, b=3
  const doc = new EditorDocument("a😀b");
  const before = stateOf(doc);

  assert.throws(
    () => doc.transact((tx) => tx.replace(2, 3, "x")),
    SurrogatePairError,
  );
  assert.throws(
    () => doc.transact((tx) => tx.insert(2, "x")),
    SurrogatePairError,
  );
  assert.throws(
    () => doc.transact((tx) => tx.delete(1, 2)),
    SurrogatePairError,
  );
  assert.deepEqual(stateOf(doc), before);

  // Boundaries around the whole pair are fine.
  doc.transact((tx) => tx.replace(1, 3, ":smile:"));
  assert.equal(doc.getText(), "a:smile:b");
});

test("rejects replacement text with unpaired boundary surrogates", () => {
  const doc = new EditorDocument("ab");
  assert.throws(
    () => doc.transact((tx) => tx.insert(1, "\uDE00")),
    SurrogatePairError,
  );
  assert.throws(
    () => doc.transact((tx) => tx.insert(1, "\uD83D")),
    SurrogatePairError,
  );
  assert.equal(doc.getText(), "ab");
  // A complete pair is accepted.
  doc.transact((tx) => tx.insert(1, "😀"));
  assert.equal(doc.getText(), "a😀b");
});

test("out-of-bounds and inverted ranges are rejected", () => {
  const doc = new EditorDocument("abc");
  assert.throws(
    () => doc.transact((tx) => tx.replace(0, 4, "x")),
    EditOutOfBoundsError,
  );
  assert.throws(
    () => doc.transact((tx) => tx.replace(-1, 2, "x")),
    EditOutOfBoundsError,
  );
  assert.throws(
    () => doc.transact((tx) => tx.replace(2, 1, "x")),
    EditOutOfBoundsError,
  );
  assert.throws(
    () => doc.transact((tx) => tx.replace(0, 1.5, "x")),
    EditOutOfBoundsError,
  );
  assert.equal(doc.getText(), "abc");
});

test("CRLF and LF line endings: offsets stay in UTF-16 units", () => {
  const doc = new EditorDocument("one\r\ntwo\nthree\r\n");
  // "one"=0..3, \r\n=3..5, "two"=5..8, \n=8..9, "three"=9..14, \r\n=14..16
  assert.equal(doc.lineCount, 4);
  assert.deepEqual(doc.positionAt(5), { line: 1, character: 0 });
  assert.deepEqual(doc.positionAt(9), { line: 2, character: 0 });
  assert.equal(doc.offsetAt({ line: 1, character: 0 }), 5);
  assert.equal(doc.offsetAt({ line: 2, character: 0 }), 9);
  // character beyond line content clamps to end of line (before the break)
  assert.equal(doc.offsetAt({ line: 0, character: 99 }), 3);

  doc.transact((tx) => {
    tx.replace(3, 5, "\n"); // CRLF -> LF
    tx.replace(9, 14, "THREE"); // baseline coords, unaffected by the first edit
  });
  assert.equal(doc.getText(), "one\ntwo\nTHREE\r\n");
});

test("editing across a CRLF boundary", () => {
  const doc = new EditorDocument("foo\r\nbar");
  doc.transact((tx) => tx.replace(2, 6, "")); // delete "o\r\nb"
  assert.equal(doc.getText(), "foar");
});

test("selections and annotations are remapped through length-changing edits", () => {
  const doc = new EditorDocument("foo bar baz");
  doc.setSelections([{ anchor: 8, head: 11 }]); // "baz"
  doc.setAnnotations([
    { id: "ann-bar", range: { start: 4, end: 7 } },
    { id: "ann-baz", range: { start: 8, end: 11 } },
  ]);
  doc.transact((tx) => tx.replace(0, 3, "longfoo")); // +4
  assert.equal(doc.getText(), "longfoo bar baz");
  assert.deepEqual(doc.getSelections(), [{ anchor: 12, head: 15 }]);
  assert.deepEqual(doc.getAnnotations(), [
    { id: "ann-bar", range: { start: 8, end: 11 } },
    { id: "ann-baz", range: { start: 12, end: 15 } },
  ]);
});

test("annotation covering a replaced region adjusts to the replacement", () => {
  const doc = new EditorDocument("foo bar baz");
  doc.setAnnotations([{ id: "a", range: { start: 4, end: 7 } }]); // "bar"
  doc.transact((tx) => tx.replace(4, 7, "x"));
  assert.deepEqual(doc.getAnnotations(), [
    { id: "a", range: { start: 4, end: 5 } },
  ]);
});

test("typing at a cursor moves the cursor after the inserted text", () => {
  const doc = new EditorDocument("ac");
  doc.setSelections([{ anchor: 1, head: 1 }]);
  doc.transact((tx) => tx.insert(1, "b"));
  assert.equal(doc.getText(), "abc");
  assert.deepEqual(doc.getSelections(), [{ anchor: 2, head: 2 }]);
});

test("staged selections are interpreted in baseline coordinates and remapped", () => {
  const doc = new EditorDocument("hello world");
  doc.transact((tx) => {
    tx.replace(0, 5, "hi"); // -3
    tx.setSelections([{ anchor: 6, head: 11 }]); // "world" in baseline coords
  });
  assert.deepEqual(doc.getSelections(), [{ anchor: 3, head: 8 }]);
  assert.equal(doc.getText().slice(3, 8), "world");
});

test("transaction can add and remove annotations", () => {
  const doc = new EditorDocument("hello world");
  doc.setAnnotations([{ id: "old", range: { start: 0, end: 5 } }]);
  doc.transact((tx) => {
    tx.removeAnnotation("old");
    tx.addAnnotation({ id: "new", range: { start: 6, end: 11 }, data: { kind: "warn" } });
  });
  assert.deepEqual(doc.getAnnotations(), [
    { id: "new", range: { start: 6, end: 11 }, data: { kind: "warn" } },
  ]);
});

test("a throwing callback restores text, selections, annotations and undo records", () => {
  const doc = new EditorDocument("hello world");
  const selections: Selection[] = [{ anchor: 0, head: 5 }];
  const annotations: Annotation[] = [{ id: "a1", range: { start: 6, end: 11 } }];
  doc.setSelections(selections);
  doc.setAnnotations(annotations);

  // One successful transaction first, so the undo stack is non-empty.
  doc.transact((tx) => tx.insert(11, "!"));
  const before = stateOf(doc);
  assert.equal(before.undoDepth, 1);

  const boom = new Error("boom");
  assert.throws(
    () =>
      doc.transact((tx) => {
        tx.replace(0, 5, "goodbye");
        tx.insert(0, ">> ");
        tx.setSelections([{ anchor: 1, head: 2 }]);
        tx.addAnnotation({ id: "temp", range: { start: 0, end: 3 } });
        tx.removeAnnotation("a1");
        throw boom;
      }),
    (err: unknown) => err === boom,
  );

  // All four state categories are exactly as before the transaction.
  assert.deepEqual(stateOf(doc), before);

  // And undo still replays the pre-failure history correctly.
  assert.equal(doc.undo(), true);
  assert.equal(doc.getText(), "hello world");
  assert.deepEqual(doc.getSelections(), selections);
  assert.deepEqual(doc.getAnnotations(), annotations);
});

test("validation failure mid-transaction also restores everything", () => {
  const doc = new EditorDocument("hello world");
  doc.setSelections([{ anchor: 2, head: 4 }]);
  const before = stateOf(doc);
  assert.throws(
    () =>
      doc.transact((tx) => {
        tx.replace(0, 2, "HI"); // valid
        tx.replace(1, 5, "x"); // conflicts with the first edit
      }),
    EditConflictError,
  );
  assert.deepEqual(stateOf(doc), before);
});

test("a successful transaction records exactly one undo entry and undo restores all state", () => {
  const doc = new EditorDocument("hello world");
  doc.setSelections([{ anchor: 0, head: 5 }]);
  doc.setAnnotations([{ id: "a1", range: { start: 6, end: 11 } }]);

  doc.transact((tx) => {
    tx.replace(0, 5, "hi");
    tx.setSelections([{ anchor: 0, head: 2 }]);
    tx.removeAnnotation("a1");
    tx.addAnnotation({ id: "a2", range: { start: 0, end: 2 } });
  });
  assert.equal(doc.undoDepth, 1);
  assert.equal(doc.getText(), "hi world");

  assert.equal(doc.undo(), true);
  assert.equal(doc.getText(), "hello world");
  assert.deepEqual(doc.getSelections(), [{ anchor: 0, head: 5 }]);
  assert.deepEqual(doc.getAnnotations(), [
    { id: "a1", range: { start: 6, end: 11 } },
  ]);
  assert.equal(doc.undoDepth, 0);
  assert.equal(doc.undo(), false);
});

test("an empty transaction records no undo entry", () => {
  const doc = new EditorDocument("abc");
  doc.transact(() => {});
  assert.equal(doc.undoDepth, 0);
  assert.equal(doc.getText(), "abc");
});

test("nested transactions are rejected and state stays intact", () => {
  const doc = new EditorDocument("abc");
  const before = stateOf(doc);
  assert.throws(
    () =>
      doc.transact(() => {
        doc.transact((tx) => tx.insert(0, "x"));
      }),
    NestedTransactionError,
  );
  assert.deepEqual(stateOf(doc), before);
});

test("multi-cursor session with emoji and CRLF combined", () => {
  const doc = new EditorDocument("😀 one\r\n😀 two\r\n😀 three");
  // Cursors on the emoji at each line start: 0, 8, 16 (😀 = 2 units, \r\n = 2 units)
  doc.setSelections([
    { anchor: 0, head: 2 },
    { anchor: 8, head: 10 },
    { anchor: 16, head: 18 },
  ]);
  doc.transact((tx) => {
    tx.replace(0, 2, "✓"); // each replacement shrinks by 1
    tx.replace(8, 10, "✓");
    tx.replace(16, 18, "✓");
  });
  assert.equal(doc.getText(), "✓ one\r\n✓ two\r\n✓ three");
  assert.deepEqual(doc.getSelections(), [
    { anchor: 0, head: 1 },
    { anchor: 7, head: 8 },
    { anchor: 14, head: 15 },
  ]);
});

test("getSelections/getAnnotations return defensive copies", () => {
  const doc = new EditorDocument("abc");
  doc.setSelections([{ anchor: 0, head: 1 }]);
  const sels = doc.getSelections();
  (sels[0] as { anchor: number }).anchor = 99;
  assert.deepEqual(doc.getSelections(), [{ anchor: 0, head: 1 }]);
});
