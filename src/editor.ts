// editor.ts — multi-selection indent / outdent / tab insertion.
//
// Document model: a plain string with '\n' line endings plus a list of
// selections. A selection is { anchor, caret }; it is backward when
// caret < anchor and a caret (cursor) when anchor === caret.
//
// Semantics:
//  - insertTab: when every selection is a caret, insert spaces at each
//    caret up to the next tab stop (visual column, existing tabs count
//    visually). When any selection is non-empty, behave like indentLines.
//  - indentLines: for every line touched by a selection, insert spaces at
//    the line start so the existing indent reaches the next tab stop.
//  - outdentLines: for every touched line, remove at most one tab stop of
//    leading whitespace (see visual.outdentCharCount).
//  - A line shared by several selections is edited exactly once.
//  - A non-empty selection whose end sits exactly at a line start does not
//    touch that line.
//  - Every operation returns an EditRecord; undo(record) restores the
//    previous text byte for byte and the previous selections.

import { applyEdits, invertEdits, mapOffset } from './edits';
import type { AppliedEdit, Bias, Edit } from './edits';
import { TAB_SIZE, indentWidth, nextTabStop, outdentCharCount, visualWidth } from './visual';

export interface Selection {
  anchor: number;
  caret: number;
}

export interface EditorState {
  text: string;
  selections: Selection[];
}

export interface EditRecord {
  forward: AppliedEdit[];
  before: Selection[];
  after: Selection[];
}

export interface OpResult {
  state: EditorState;
  record: EditRecord;
}

function lineStarts(text: string): number[] {
  const starts = [0];
  for (let i = 0; i < text.length; i++) {
    if (text.charCodeAt(i) === 10) {
      starts.push(i + 1);
    }
  }
  return starts;
}

function lineOf(starts: number[], pos: number): number {
  let lo = 0;
  let hi = starts.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (starts[mid] <= pos) {
      lo = mid;
    } else {
      hi = mid - 1;
    }
  }
  return lo;
}

function lineEnd(text: string, start: number): number {
  const nl = text.indexOf('\n', start);
  return nl === -1 ? text.length : nl;
}

// Sorted, de-duplicated list of lines touched by any selection.
function collectTouchedLines(starts: number[], selections: Selection[]): number[] {
  const seen = new Set();
  for (const sel of selections) {
    const s = Math.min(sel.anchor, sel.caret);
    const e = Math.max(sel.anchor, sel.caret);
    const first = lineOf(starts, s);
    let last = lineOf(starts, e);
    if (e > s && e === starts[last]) {
      last -= 1;
    }
    for (let l = first; l <= last; l++) {
      seen.add(l);
    }
  }
  return Array.from(seen).sort((a, b) => a - b);
}

function snapshot(selections: Selection[]): Selection[] {
  return selections.map((sel) => {
    return { anchor: sel.anchor, caret: sel.caret };
  });
}

function finish(state: EditorState, edits: Edit[], bias: Bias): OpResult {
  const result = applyEdits(state.text, edits);
  const selections: Selection[] = [];
  for (const sel of state.selections) {
    selections.push({
      anchor: mapOffset(sel.anchor, edits, bias),
      caret: mapOffset(sel.caret, edits, bias),
    });
  }
  return {
    state: { text: result.text, selections },
    record: { forward: result.applied, before: snapshot(state.selections), after: snapshot(selections) },
  };
}

function insertTabAtCarets(state: EditorState, tabSize: number): OpResult {
  const starts = lineStarts(state.text);
  const seen = new Set();
  const positions: number[] = [];
  for (const sel of state.selections) {
    if (!seen.has(sel.caret)) {
      seen.add(sel.caret);
      positions.push(sel.caret);
    }
  }
  positions.sort((a, b) => a - b);
  const edits: Edit[] = [];
  for (const pos of positions) {
    const line = lineOf(starts, pos);
    const prefix = state.text.slice(starts[line], pos);
    const col = visualWidth(prefix, tabSize);
    const spaces = nextTabStop(col, tabSize) - col;
    edits.push({ start: pos, end: pos, inserted: ' '.repeat(spaces) });
  }
  return finish(state, edits, 'end');
}

function insertTab(state: EditorState, tabSize: number = TAB_SIZE): OpResult {
  const allCarets = state.selections.every((sel) => sel.anchor === sel.caret);
  if (allCarets) {
    return insertTabAtCarets(state, tabSize);
  }
  return indentLines(state, tabSize);
}

function indentLines(state: EditorState, tabSize: number = TAB_SIZE): OpResult {
  const starts = lineStarts(state.text);
  const touched = collectTouchedLines(starts, state.selections);
  const edits: Edit[] = [];
  for (const line of touched) {
    const start = starts[line];
    const lineText = state.text.slice(start, lineEnd(state.text, start));
    const w = indentWidth(lineText, tabSize);
    const spaces = nextTabStop(w, tabSize) - w;
    edits.push({ start, end: start, inserted: ' '.repeat(spaces) });
  }
  return finish(state, edits, 'start');
}

function outdentLines(state: EditorState, tabSize: number = TAB_SIZE): OpResult {
  const starts = lineStarts(state.text);
  const touched = collectTouchedLines(starts, state.selections);
  const edits: Edit[] = [];
  for (const line of touched) {
    const start = starts[line];
    const lineText = state.text.slice(start, lineEnd(state.text, start));
    const count = outdentCharCount(lineText, tabSize);
    if (count > 0) {
      edits.push({ start, end: start + count, inserted: '' });
    }
  }
  return finish(state, edits, 'start');
}

function undo(state: EditorState, record: EditRecord): EditorState {
  const inverse = invertEdits(record.forward);
  const result = applyEdits(state.text, inverse);
  return { text: result.text, selections: snapshot(record.before) };
}

function redo(state: EditorState, record: EditRecord): EditorState {
  const edits: Edit[] = record.forward.map((e) => {
    return { start: e.start, end: e.end, inserted: e.inserted };
  });
  const result = applyEdits(state.text, edits);
  return { text: result.text, selections: snapshot(record.after) };
}

export { insertTab, indentLines, outdentLines, undo, redo };
