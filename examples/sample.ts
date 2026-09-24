import { AnnotationStore, positionAtOffset } from '../src/index.ts';

function show(label: string, store: AnnotationStore, id: string): void {
  const a = store.getAnnotation(id)!;
  const covered = store.getText().slice(a.start, a.end);
  console.log(
    `${label}\n  text=${JSON.stringify(store.getText())}` +
      `  range=[${a.start}, ${a.end})  covered=${JSON.stringify(covered)}`,
  );
}

// 1. Insertion at the left endpoint of [2,4): inward vs outward affinity.
const inward = new AnnotationStore('abcdef');
inward.addAnnotation({ id: 'r', start: 2, end: 4, startAffinity: 'inward', endAffinity: 'inward' });
inward.applyEdits([{ id: 1, offset: 2, deleteCount: 0, insertText: 'XY' }]);
show('1a. insert "XY" at left end, startAffinity=inward (excluded)', inward, 'r');

const outward = new AnnotationStore('abcdef');
outward.addAnnotation({ id: 'r', start: 2, end: 4, startAffinity: 'outward', endAffinity: 'inward' });
outward.applyEdits([{ id: 1, offset: 2, deleteCount: 0, insertText: 'XY' }]);
show('1b. insert "XY" at left end, startAffinity=outward (included)', outward, 'r');

// 2. Deleting everything the annotation covers keeps a zero-width anchor.
const collapsed = new AnnotationStore('abcdef');
collapsed.addAnnotation({ id: 'r', start: 2, end: 4, startAffinity: 'inward', endAffinity: 'inward' });
collapsed.applyEdits([{ id: 1, offset: 1, deleteCount: 4 }]);
show('2. delete [1,5) swallows the range -> zero-width anchor', collapsed, 'r');

// 3. Batched edits at the same offset apply in operation-ID order.
const batch = new AnnotationStore('ac');
batch.applyEdits([
  { id: 30, offset: 1, deleteCount: 0, insertText: '3' },
  { id: 10, offset: 1, deleteCount: 0, insertText: '1' },
  { id: 20, offset: 1, deleteCount: 0, insertText: '2' },
]);
console.log(`3. same-offset inserts ordered by op id -> ${JSON.stringify(batch.getText())}`);

// 4. Undo restores the original anchors.
const undoable = new AnnotationStore('hello world');
undoable.addAnnotation({ id: 'w', start: 6, end: 11, startAffinity: 'inward', endAffinity: 'outward' });
undoable.applyEdits([
  { id: 2, offset: 11, deleteCount: 0, insertText: '!' },
  { id: 1, offset: 0, deleteCount: 5, insertText: 'Hi' },
]);
show('4a. after batch (replace greeting, append "!")', undoable, 'w');
undoable.undo();
show('4b. after undo, anchors restored', undoable, 'w');

// 5. UTF-16 offsets and CRLF line endings.
const crlf = new AnnotationStore('one\r\ntwo\r\nthree');
crlf.addAnnotation({ id: 'L', start: 5, end: 8, startAffinity: 'inward', endAffinity: 'inward' });
crlf.applyEdits([{ id: 1, offset: 0, deleteCount: 0, insertText: '\u{1F600}\r\n' }]);
show('5. CRLF doc, emoji line inserted (emoji = 2 UTF-16 units)', crlf, 'L');
console.log(`   anchor position: ${JSON.stringify(positionAtOffset(crlf.getText(), crlf.getAnnotation('L')!.start))}`);
