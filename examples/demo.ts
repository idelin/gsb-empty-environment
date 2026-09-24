/**
 * Runnable demo:  node examples/demo.ts
 * Simulates an IME composition interleaved with remote edits, undo and blur.
 */
import { Editor } from '../src/editor.ts';

function show(label: string, ed: Editor): void {
  console.log(
    `${label.padEnd(38)} formal=${JSON.stringify(ed.getText())}  ` +
      `projection=${JSON.stringify(ed.getProjection())}  ` +
      `history=[${ed.historyKinds().join(',')}] composing=${ed.isComposing()}`,
  );
}

console.log('--- Scenario 1: composition with 10 updates -> ONE undo unit ---');
const ed = new Editor('hello ');
ed.setSelection(6, 6);
const id = ed.compositionStart();
console.log(`compositionStart() -> generation ${id}`);
for (const t of ['n', 'ni', 'nih', 'に', 'にほ', 'にほん', 'にほんご', '日本語', '日本語!', '日本語!!']) {
  ed.compositionUpdate(id, t);
}
show('after 10 updates', ed);
ed.compositionCommit(id, '日本語');
show('after commit', ed);
ed.undo();
show('after ONE undo', ed);
ed.redo();
show('after redo', ed);

console.log('\n--- Scenario 2: non-conflicting remote edit transforms the range ---');
const ed2 = new Editor('world');
ed2.setSelection(0, 0);
const id2 = ed2.compositionStart();
ed2.compositionUpdate(id2, 'こんにちは');
ed2.applyRemoteEdit({ start: 5, end: 5, text: '!' }); // after the composition: no conflict
show('remote insert at end', ed2);
ed2.applyRemoteEdit({ start: 0, end: 0, text: '>> ' }); // at composition start boundary: no conflict
show('remote insert at boundary', ed2);
ed2.compositionCommit(id2);
show('commit', ed2);

console.log('\n--- Scenario 3: conflicting remote edit cancels the composition ---');
const ed3 = new Editor('abcdef');
ed3.setSelection(3, 3);
const id3 = ed3.compositionStart();
ed3.compositionUpdate(id3, 'IME');
show('composing', ed3);
ed3.applyRemoteEdit({ start: 2, end: 4, text: '_' }); // overlaps the composition point
show('conflicting remote edit', ed3);
console.log(`late update accepted? ${ed3.compositionUpdate(id3, 'late')}`);

console.log('\n--- Scenario 4: cancel keeps remote content, restores selection ---');
const ed4 = new Editor('core');
ed4.setSelection(1, 3); // selects "or"
const id4 = ed4.compositionStart();
ed4.compositionUpdate(id4, 'ORE');
ed4.applyRemoteEdit({ start: 4, end: 4, text: ' [remote]' });
ed4.compositionCancel(id4);
show('after cancel', ed4);
console.log(`selection restored: ${JSON.stringify(ed4.getSelection())}`);

console.log('\n--- Scenario 5: stale generation cannot pollute; emoji + CRLF ---');
const ed5 = new Editor('a😀\r\nb');
ed5.setSelection(5, 5); // after the CRLF (CRLF = 2 UTF-16 units), before "b"
const g1 = ed5.compositionStart();
ed5.compositionUpdate(g1, 'OLD');
const g2 = ed5.compositionStart(); // restart -> new generation
console.log(`stale update accepted? ${ed5.compositionUpdate(g1, 'POLLUTE')}`);
ed5.compositionUpdate(g2, '新');
ed5.compositionCommit(g2);
show('commit new generation', ed5);
ed5.setSelection(2, 2); // mid CRLF? no: 2 is the high-surrogate... actually 😀 is 1..2, \r=3 \n=4
console.log('clamp mid-surrogate offset 2 -> composition range:');
ed5.setSelection(2, 2);
const g3 = ed5.compositionStart();
console.log(`  range=${JSON.stringify(ed5.getCompositionRange())} (snapped to pair start 1)`);
ed5.compositionCancel(g3);
