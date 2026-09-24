/**
 * Runnable tour of the folding-projection library.
 * Run with: npm run demo
 */
import { FoldedDocument, normalizeFolds } from "../src/index.ts";

const show = (label: string, value: unknown) =>
  console.log(label.padEnd(34), value);

// --- 1. Nested folds & projection ----------------------------------------
const source = "function f() {\n  if (x) {\n    work();\n  }\n}";
const doc = new FoldedDocument(source);
doc.addFold(15, 41); // outer: the if-block body through closing brace
doc.addFold(26, 37); // inner: the "    work();" line

show("original:", JSON.stringify(source));
show("outer collapsed:", JSON.stringify(doc.visibleText));

doc.expandAt(15);
show("outer expanded:", JSON.stringify(doc.visibleText));
console.log("  -> inner fold stayed collapsed:", doc.foldTree[0].children[0].collapsed);

// --- 2. Offset mapping ----------------------------------------------------
const v = doc.visibleText;
const at = v.indexOf("⋯");
show("placeholder visible offset:", at);
show("-> original range:", doc.toOriginalRange(at, at + 1));
const sel = doc.toOriginalRange(2, v.length - 2);
show("cross-placeholder selection:", sel);
show("-> original text:", JSON.stringify(source.slice(sel[0], sel[1])));

// --- 3. Partial intersections: normalize or reject ------------------------
const messy = [
  { start: 15, end: 30 },
  { start: 26, end: 41 },
]; // partially intersecting
show("normalizeFolds:", normalizeFolds(messy));
try {
  const strict = new FoldedDocument(source);
  strict.addFold(15, 30);
  strict.addFold(26, 41);
} catch (err) {
  show("rejected:", (err as Error).message);
}

// --- 4. UTF-16 safety ------------------------------------------------------
const emojiDoc = new FoldedDocument("a😀b😀c");
const region = emojiDoc.addFold(2, 4); // would cut both emoji; snapped outward
show("emoji fold snapped to:", region);
show("emoji projection:", JSON.stringify(emojiDoc.visibleText));

// --- 5. CRLF documents -----------------------------------------------------
const crlf = new FoldedDocument("one\r\ntwo\r\nthree");
crlf.addFold(crlf.originalOffsetAt({ line: 1, character: 0 }), crlf.text.length);
show("CRLF projection:", JSON.stringify(crlf.visibleText));
show("CRLF position of ⋯:", crlf.visiblePositionAt(crlf.visibleText.indexOf("⋯")));

// --- 6. Editing moves placeholders, keeps nesting --------------------------
const edit = new FoldedDocument("alpha beta gamma delta epsilon");
edit.addFold(6, 22); // outer
edit.addFold(11, 16); // inner
edit.applyVisibleEdit(0, 0, ">> ");
show("after insert before fold:", JSON.stringify(edit.visibleText));
show("folds shifted to:", edit.folds);
edit.expandAt(6 + 3);
edit.applyVisibleEdit(edit.toVisibleOffset(16), edit.toVisibleOffset(16), "!");
show("typed inside outer fold:", JSON.stringify(edit.visibleText));
show("nesting preserved:", edit.foldTree.map((n) => [n.children.length, n.children[0].collapsed]));
const ph = edit.visibleText.indexOf("⋯");
edit.applyVisibleEdit(ph, ph + 1, ""); // delete the inner placeholder
show("placeholder deleted:", JSON.stringify(edit.visibleText));
show("remaining folds:", edit.folds);
