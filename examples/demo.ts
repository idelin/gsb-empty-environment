import {
  EditorDocument,
  EditConflictError,
  SurrogatePairError,
} from "../src/index.ts";

function show(label: string, doc: EditorDocument): void {
  console.log(`\n=== ${label} ===`);
  console.log(`text:        ${JSON.stringify(doc.getText())}`);
  console.log(`selections:  ${JSON.stringify(doc.getSelections())}`);
  console.log(`annotations: ${JSON.stringify(doc.getAnnotations())}`);
  console.log(`undo depth:  ${doc.undoDepth}`);
}

// 1. Multi-cursor transaction: all ranges share ONE baseline coordinate
//    system. The CRLF line ending counts as 2 UTF-16 offsets.
const doc = new EditorDocument("foo 😀 bar\r\nbaz quux");
// offsets: foo=0..3, 😀=4..6, bar=7..10, \r\n=10..12, baz=12..15, quux=16..20
doc.setSelections([{ anchor: 0, head: 3 }, { anchor: 12, head: 15 }]);
doc.setAnnotations([{ id: "review", range: { start: 16, end: 20 } }]);
show("before transaction", doc);

doc.transact((tx) => {
  tx.replace(0, 3, "FOO");        // cursor 1 word
  tx.replace(4, 6, ":smile:");    // emoji -> shortcode (grows by 5)
  tx.replace(12, 15, "BAZ");      // cursor 2 word; baseline coords, unaffected
  tx.replace(16, 20, "QUUX!!");   // grows by 2, annotation rides along
});
show("after multi-cursor transaction", doc);

// 2. Undo restores text, selections and annotations in one step.
doc.undo();
show("after undo", doc);

// 3. Overlapping replacements fail BEFORE anything is written.
try {
  doc.transact((tx) => {
    tx.replace(0, 3, "x");
    tx.replace(2, 4, "y"); // overlaps [0, 3)
  });
} catch (error) {
  console.log(`\n=== overlapping edits rejected ===`);
  console.log(`${(error as Error).name}: ${(error as Error).message}`);
  console.log(`conflict is EditConflictError: ${error instanceof EditConflictError}`);
}
show("state after rejected overlap (unchanged)", doc);

// 4. A throwing callback rolls back ALL FOUR state categories.
try {
  doc.transact((tx) => {
    tx.replace(0, 3, "CHANGED");
    tx.setSelections([{ anchor: 1, head: 1 }]);
    tx.removeAnnotation("review");
    throw new Error("callback blew up");
  });
} catch (error) {
  console.log(`\n=== throwing callback rolled back ===`);
  console.log(`caught: ${(error as Error).message}`);
}
show("state after rollback (identical to before)", doc);

// 5. Surrogate pairs may never be split.
try {
  doc.transact((tx) => tx.insert(5, "x")); // offset 5 sits inside "😀"
} catch (error) {
  console.log(`\n=== surrogate split rejected ===`);
  console.log(`${(error as Error).name}: ${(error as Error).message}`);
  console.log(`is SurrogatePairError: ${error instanceof SurrogatePairError}`);
}

// 6. LF / CRLF line-position helpers.
console.log(`\n=== line/character helpers (LF + CRLF) ===`);
const mixed = new EditorDocument("one\r\ntwo\nthree");
for (const pos of [{ line: 0, character: 1 }, { line: 1, character: 0 }, { line: 2, character: 5 }]) {
  const offset = mixed.offsetAt(pos);
  console.log(`offsetAt(${JSON.stringify(pos)}) = ${offset}, roundtrip = ${JSON.stringify(mixed.positionAt(offset))}`);
}
