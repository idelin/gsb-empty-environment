/**
 * Runnable demo:  node examples/demo.ts   (or: npm run demo)
 *
 * Walks through an atomic multi-cursor transaction on a CRLF document that
 * contains an astral (surrogate-pair) character, then shows conflict
 * rejection, rollback on callback failure, and undo.
 */
import { Editor, EditConflictError } from '../src/index.ts';

function show(label: string, editor: Editor): void {
  console.log(`\n=== ${label} ===`);
  console.log('text:       ', JSON.stringify(editor.text));
  console.log('selections: ', JSON.stringify(editor.selections));
  console.log(
    'annotations:',
    JSON.stringify(
      editor.annotations.map((a) => ({ id: a.id, range: a.range })),
    ),
  );
  console.log('undo depth: ', editor.undoDepth);
}

// A CRLF document with an emoji (2 UTF-16 code units) on line 1.
const editor = new Editor('const a = 1;\r\nconst b = "😀";\r\nconst c = 3;\r\n');
editor.setSelections([{ anchor: 6, head: 7 }]); // around "a"
editor.addAnnotation({ id: 'lint:c', range: { start: 37, end: 38 } }); // "c"
show('initial document (CRLF, emoji)', editor);

// --- 1. One atomic transaction, three cursors, all in baseline offsets. ---
// Line 0: "const a = 1;"  -> offsets 0..11,  CRLF at 12..13
// Line 1: "const b = "😀";" -> offsets 14..28 (😀 = 25..26), CRLF 29..30
// Line 2: "const c = 3;"  -> offsets 31..42, CRLF 43..44
editor.transaction((tx) => {
  tx.replace(6, 7, 'alpha'); // rename a  (grows line 0)
  tx.replace(20, 21, 'beta'); // rename b  (baseline offsets still valid!)
  tx.replace(37, 38, 'gamma'); // rename c
  // Selections staged in baseline coordinates, mapped on commit.
  tx.setSelections([
    { anchor: 6, head: 11 }, // selects "alpha" after mapping
    { anchor: 37, head: 37 }, // caret where "c" used to start
  ]);
  // New annotation staged in baseline coordinates.
  tx.addAnnotation({ id: 'todo', range: { start: 14, end: 19 } });
});
show('after 3-cursor transaction', editor);

// --- 2. Conflicting edits fail BEFORE anything is written. ---
try {
  editor.transaction((tx) => {
    tx.replace(0, 8, 'let');
    tx.replace(5, 12, 'var'); // overlaps the first edit
  });
} catch (error) {
  console.log('\n=== overlapping edits rejected ===');
  console.log((error as EditConflictError).toString());
  console.log('text unchanged:', JSON.stringify(editor.text));
}

// --- 3. A throwing callback rolls back ALL four state categories. ---
try {
  editor.transaction((tx) => {
    tx.insert(0, 'BROKEN ');
    tx.setSelections([]);
    tx.removeAnnotation('lint:c');
    throw new Error('simulated failure mid-transaction');
  });
} catch (error) {
  console.log('\n=== callback threw, everything restored ===');
  console.log((error as Error).message);
  show('state identical to before the failed transaction', editor);
}

// --- 4. Undo restores text, selections and annotations together. ---
editor.undo();
show('after undo of the 3-cursor transaction', editor);
