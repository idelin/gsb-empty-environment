import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Editor } from '../src/editor.ts';
import { clampOffset } from '../src/utf16.ts';

test('ten updates then commit produce exactly one undo unit', () => {
  const ed = new Editor('hello');
  ed.setSelection(5, 5);
  const id = ed.compositionStart();
  const base = ed.historyKinds().length;
  for (const t of ['k', 'ka', 'kan', 'kana', 'か', 'かな', 'かな文', 'かな文字', 'かなもじ', 'かな文字!']) {
    assert.equal(ed.compositionUpdate(id, t), true);
  }
  assert.equal(ed.historyKinds().length, base, 'updates must not touch formal history');
  assert.equal(ed.getText(), 'hello', 'updates must not touch the formal document');
  assert.equal(ed.getProjection(), 'helloかな文字!');
  assert.equal(ed.compositionCommit(id), true);
  assert.deepEqual(ed.historyKinds(), ['commit']);
  assert.equal(ed.getText(), 'helloかな文字!');
  assert.equal(ed.undo(), true);
  assert.equal(ed.getText(), 'hello', 'single undo restores pre-composition text');
  assert.equal(ed.canUndo(), false);
  assert.equal(ed.redo(), true);
  assert.equal(ed.getText(), 'helloかな文字!');
});

test('cancel restores pre-composition text and selection', () => {
  const ed = new Editor('hello world');
  ed.setSelection(2, 4); // selects "ll"
  const id = ed.compositionStart();
  ed.compositionUpdate(id, 'LLL');
  assert.equal(ed.getProjection(), 'heLLLo world');
  assert.equal(ed.compositionCancel(id), true);
  assert.equal(ed.getText(), 'hello world');
  assert.deepEqual(ed.getSelection(), { anchor: 2, focus: 4 });
  assert.equal(ed.canUndo(), false, 'cancel pushes no undo entry');
});

test('duplicate commit and late update never insert twice', () => {
  const ed = new Editor('ab');
  ed.setSelection(1, 1);
  const id = ed.compositionStart();
  ed.compositionUpdate(id, 'X');
  assert.equal(ed.compositionCommit(id), true);
  assert.equal(ed.getText(), 'aXb');
  assert.equal(ed.compositionCommit(id), false, 'repeated commit is a no-op');
  assert.equal(ed.compositionCommit(id, 'Y'), false);
  assert.equal(ed.compositionUpdate(id, 'Z'), false, 'late update is a no-op');
  assert.equal(ed.compositionCancel(id), false, 'late cancel is a no-op');
  assert.equal(ed.getText(), 'aXb');
});

test('generation ids: stale callbacks cannot pollute a newer composition', () => {
  const ed = new Editor('doc');
  ed.setSelection(3, 3);
  const oldId = ed.compositionStart();
  ed.compositionUpdate(oldId, 'OLD');
  const newId = ed.compositionStart(); // implicitly cancels the old one (R6)
  assert.notEqual(newId, oldId);
  assert.equal(ed.compositionUpdate(oldId, 'POLLUTE'), false);
  assert.equal(ed.compositionCommit(oldId, 'POLLUTE'), false);
  assert.equal(ed.getProjection(), 'doc');
  ed.compositionUpdate(newId, 'NEW');
  assert.equal(ed.getProjection(), 'docNEW');
  ed.compositionCommit(newId);
  assert.equal(ed.getText(), 'docNEW');
});

test('non-conflicting remote edit transforms the composition range', () => {
  const ed = new Editor('hello world');
  ed.setSelection(6, 6); // caret before "world"
  const id = ed.compositionStart();
  ed.compositionUpdate(id, 'W');
  ed.applyRemoteEdit({ start: 0, end: 0, text: 'BIG ' });
  assert.equal(ed.isComposing(), true);
  assert.deepEqual(ed.getCompositionRange(), { start: 10, end: 10 });
  assert.equal(ed.getProjection(), 'BIG hello Wworld');
  ed.compositionCommit(id, 'WIDE');
  assert.equal(ed.getText(), 'BIG hello WIDEworld');
});

test('conflicting remote edit cancels the composition (external edit wins)', () => {
  const ed = new Editor('hello world');
  ed.setSelection(6, 6);
  const id = ed.compositionStart();
  ed.compositionUpdate(id, 'W');
  ed.applyRemoteEdit({ start: 5, end: 8, text: '_' }); // overlaps composition point
  assert.equal(ed.isComposing(), false);
  assert.equal(ed.getText(), 'hello_rld');
  assert.equal(ed.getProjection(), 'hello_rld');
  assert.equal(ed.compositionUpdate(id, 'late'), false);
  assert.equal(ed.getText(), 'hello_rld');
});

test('insertion exactly at a composition boundary is not a conflict', () => {
  const ed = new Editor('abcd');
  ed.setSelection(1, 3); // compose over "bc"
  const id = ed.compositionStart();
  ed.compositionUpdate(id, 'BC');
  ed.applyRemoteEdit({ start: 1, end: 1, text: '<' }); // at start boundary
  ed.applyRemoteEdit({ start: 4, end: 4, text: '>' }); // at end boundary (shifted)
  assert.equal(ed.isComposing(), true);
  assert.deepEqual(ed.getCompositionRange(), { start: 2, end: 4 });
  ed.compositionCommit(id);
  assert.equal(ed.getText(), 'a<BC>d');
});

test('cancel never undoes remote content', () => {
  const ed = new Editor('middle');
  ed.setSelection(6, 6);
  const id = ed.compositionStart();
  ed.compositionUpdate(id, 'IME');
  ed.applyRemoteEdit({ start: 0, end: 0, text: 'R1 ' });
  ed.applyRemoteEdit({ start: 0, end: 0, text: 'R2 ' });
  assert.equal(ed.compositionCancel(id), true);
  assert.equal(ed.getText(), 'R2 R1 middle', 'remote edits survive the cancel');
  assert.equal(ed.historyKinds().filter((k) => k === 'remote').length, 2);
});

test('undo/redo interleaved with a composition transforms or cancels it', () => {
  const ed = new Editor('base');
  ed.applyRemoteEdit({ start: 0, end: 0, text: 'pre ' });
  ed.setSelection(9, 9); // end of "pre base"
  const id = ed.compositionStart();
  ed.compositionUpdate(id, 'X');
  // Undo the remote edit (non-conflicting): composition range shifts back.
  assert.equal(ed.undo(), true);
  assert.equal(ed.isComposing(), true);
  assert.deepEqual(ed.getCompositionRange(), { start: 4, end: 4 });
  ed.compositionCommit(id);
  assert.equal(ed.getText(), 'baseX');

  // Undo of an edit overlapping the composition cancels it (R5/R7).
  const ed2 = new Editor('base');
  ed2.applyLocalEdit({ start: 0, end: 0, text: 'TAG' });
  ed2.setSelection(4, 4); // inside the inserted "TAG|base"? caret after "TAGb"? -> between G and a? use 1: inside TAG
  ed2.setSelection(1, 1);
  const id2 = ed2.compositionStart();
  ed2.compositionUpdate(id2, 'q');
  assert.equal(ed2.undo(), true, 'undo removes TAG, overlapping the composition');
  assert.equal(ed2.isComposing(), false);
  assert.equal(ed2.getText(), 'base');
});

test('undo of a commit restores the pre-composition selection', () => {
  const ed = new Editor('hello world');
  ed.setSelection(6, 11); // select "world"
  const id = ed.compositionStart();
  ed.compositionUpdate(id, '世界');
  ed.compositionCommit(id);
  assert.equal(ed.getText(), 'hello 世界');
  ed.undo();
  assert.equal(ed.getText(), 'hello world');
  assert.deepEqual(ed.getSelection(), { anchor: 6, focus: 11 });
});

test('blur cancels the composition', () => {
  const ed = new Editor('txt');
  ed.setSelection(3, 3);
  const id = ed.compositionStart();
  ed.compositionUpdate(id, 'ime');
  ed.blur();
  assert.equal(ed.isComposing(), false);
  assert.equal(ed.getText(), 'txt');
  assert.equal(ed.compositionCommit(id), false);
});

test('surrogate pairs are never split by public offsets', () => {
  const text = 'a😀b'; // offsets: a=0, 😀=1..2, b=3
  assert.equal(clampOffset(text, 2), 1, 'mid-pair offset snaps down to pair start');
  assert.equal(clampOffset(text, 1), 1);
  assert.equal(clampOffset(text, 3), 3);

  const ed = new Editor(text);
  ed.setSelection(2, 2); // mid-pair caret -> snapped to 1
  const id = ed.compositionStart();
  assert.deepEqual(ed.getCompositionRange(), { start: 1, end: 1 });
  ed.compositionUpdate(id, 'X');
  ed.compositionCommit(id);
  assert.equal(ed.getText(), 'aX😀b');

  // Remote edit with a mid-pair offset is snapped too.
  const ed2 = new Editor(text);
  ed2.applyRemoteEdit({ start: 2, end: 2, text: 'R' });
  assert.equal(ed2.getText(), 'aR😀b');
});

test('LF and CRLF line endings are both supported verbatim', () => {
  const lf = new Editor('a\nb');
  lf.setSelection(2, 2);
  let id = lf.compositionStart();
  lf.compositionUpdate(id, 'X');
  lf.compositionCommit(id);
  assert.equal(lf.getText(), 'a\nXb');

  const crlf = new Editor('a\r\nb'); // offsets: a=0 \r=1 \n=2 b=3
  crlf.setSelection(3, 3);
  id = crlf.compositionStart();
  crlf.compositionUpdate(id, 'X');
  crlf.compositionCommit(id);
  assert.equal(crlf.getText(), 'a\r\nXb', 'CRLF counts as two UTF-16 units');

  // Selection spanning a CRLF, replaced by a composition.
  const ed = new Editor('one\r\ntwo');
  ed.setSelection(3, 5); // selects "\r\n"
  id = ed.compositionStart();
  ed.compositionUpdate(id, '\n');
  ed.compositionCommit(id);
  assert.equal(ed.getText(), 'one\ntwo');
  ed.undo();
  assert.equal(ed.getText(), 'one\r\ntwo');
});

test('composition over a selection replaces it on commit', () => {
  const ed = new Editor('the quick fox');
  ed.setSelection(4, 9); // "quick"
  const id = ed.compositionStart();
  ed.compositionUpdate(id, 'slow');
  assert.equal(ed.getProjection(), 'the slow fox');
  ed.compositionCommit(id);
  assert.equal(ed.getText(), 'the slow fox');
  assert.deepEqual(ed.getSelection(), { anchor: 8, focus: 8 });
});
