/**
 * Runnable walkthrough: node examples/demo.ts
 */
import { DiffSession } from "../src/index.ts";

function showBlocks(session: DiffSession, label: string): void {
  console.log(`\n== ${label} ==`);
  const changes = session.getChangeBlocks();
  if (changes.length === 0) {
    console.log("no differences");
    return;
  }
  for (const block of changes) {
    console.log(
      `change  A[${block.aStart}..${block.aEnd})  B[${block.bStart}..${block.bEnd})`,
    );
  }
}

// Two long texts: 200 identical lines with a few differences.
const linesA = Array.from({ length: 200 }, (_, i) => `line ${i}`);
const linesB = linesA.slice();
linesB[42] = "line 42 (edited in B)";
linesB.splice(100, 0, "inserted only in B");
delete linesA[150];
const textA = linesA.filter((l) => l !== undefined).join("\n");
const textB = linesB.join("\n");

const session = new DiffSession(textA, textB);
showBlocks(session, "initial diff");

console.log("\n== folds (context = 2) ==");
for (const fold of session.folds(2)) {
  console.log(
    `fold  A[${fold.aStart}..${fold.aEnd})  B[${fold.bStart}..${fold.bEnd})  hidden=${fold.hiddenLines}`,
  );
}

console.log("\n== bidirectional mapping ==");
console.log("A line 42  -> B line", session.aToB(42)); // changed: null
console.log("A line 60  -> B line", session.aToB(60)); // shifted by 0
console.log("B line 100 -> A line", session.bToA(100)); // inserted: null
console.log("B line 150 -> A line", session.bToA(150));
console.log("A line 151 -> B line", session.aToB(151)); // shifted by +1

// Local edit on side A: replace the deleted line region with new content.
console.log("\n== local edit on side A ==");
const lines = session.getLines("a");
const target = lines[149]; // line after the gap left by the deletion
session.applyEdit("a", {
  start: target.start,
  end: target.start,
  text: "brand new line in A\n",
});
showBlocks(session, "after inserting a line into A");

// Revert the edit: diff blocks disappear again.
session.applyEdit("a", {
  start: target.start,
  end: target.start + "brand new line in A\n".length,
  text: "",
});
showBlocks(session, "after reverting the edit");

// UTF-16 / surrogate safety demo.
console.log("\n== UTF-16 offsets and surrogate pairs ==");
const emojiSession = new DiffSession("😀 smile\nsecond\n", "😀 smile\nsecond\n");
try {
  emojiSession.applyEdit("a", { start: 1, end: 1, text: "x" }); // inside 😀
} catch (err) {
  console.log("rejected edit inside surrogate pair:", (err as Error).message);
}
emojiSession.applyEdit("a", { start: 2, end: 2, text: "!" }); // safe: after 😀
console.log("after safe edit:", JSON.stringify(emojiSession.getText("a")));

// CRLF demo.
console.log("\n== CRLF vs LF ==");
const crlf = new DiffSession("one\r\ntwo\r\n", "one\ntwo\n");
console.log("change blocks for same content with different EOLs:", crlf.getChangeBlocks().length);
