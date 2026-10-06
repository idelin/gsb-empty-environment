// reference.ts — an independent, deliberately naive implementation of the
// same editing semantics, written in a line-by-line style: split the text
// into lines, transform each line on its own, map selections in
// (line, column) space, then reassemble. It shares no code with editor.ts
// and exists purely so tests can cross-check the offset/edit-based
// implementation against it.

import type { EditorState, Selection } from './editor';

interface LineCol {
  line: number;
  col: number;
}

interface CaretInsert {
  col: number;
  spaces: number;
}

function lineStartsOf(text: string): number[] {
  const starts = [0];
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '\n') {
      starts.push(i + 1);
    }
  }
  return starts;
}

function locate(starts: number[], pos: number): LineCol {
  let line = 0;
  for (let i = 0; i < starts.length; i++) {
    if (starts[i] <= pos) {
      line = i;
    } else {
      break;
    }
  }
  return { line, col: pos - starts[line] };
}

function visualWidthOf(s: string, tabSize: number): number {
  let col = 0;
  for (const ch of s) {
    if (ch === '\t') {
      col += tabSize - (col % tabSize);
    } else {
      col += 1;
    }
  }
  return col;
}

function indentWidthOf(line: string, tabSize: number): number {
  let col = 0;
  for (const ch of line) {
    if (ch === ' ') {
      col += 1;
    } else if (ch === '\t') {
      col += tabSize - (col % tabSize);
    } else {
      break;
    }
  }
  return col;
}

function outdentCountOf(line: string, tabSize: number): number {
  let width = 0;
  let count = 0;
  for (const ch of line) {
    let w = 0;
    if (ch === ' ') {
      w = 1;
    } else if (ch === '\t') {
      w = tabSize - (width % tabSize);
    } else {
      break;
    }
    if (width + w > tabSize) {
      break;
    }
    width += w;
    count += 1;
  }
  return count;
}

function touchedFlags(lineCount: number, starts: number[], selections: Selection[]): boolean[] {
  const flags: boolean[] = [];
  for (let i = 0; i < lineCount; i++) {
    flags.push(false);
  }
  for (const sel of selections) {
    const s = Math.min(sel.anchor, sel.caret);
    const e = Math.max(sel.anchor, sel.caret);
    const first = locate(starts, s).line;
    const endLoc = locate(starts, e);
    let last = endLoc.line;
    if (e > s && endLoc.col === 0) {
      last -= 1;
    }
    for (let l = first; l <= last; l++) {
      flags[l] = true;
    }
  }
  return flags;
}

function mapPosIndent(pos: number, starts: number[], newStarts: number[], flags: boolean[], inserted: number[]): number {
  const loc = locate(starts, pos);
  let col = loc.col;
  if (flags[loc.line] && col > 0) {
    col += inserted[loc.line];
  }
  return newStarts[loc.line] + col;
}

function mapPosOutdent(pos: number, starts: number[], newStarts: number[], flags: boolean[], removed: number[]): number {
  const loc = locate(starts, pos);
  let col = loc.col;
  if (flags[loc.line] && col > 0) {
    col = Math.max(0, col - removed[loc.line]);
  }
  return newStarts[loc.line] + col;
}

function refIndentLines(state: EditorState, tabSize: number): EditorState {
  const lines = state.text.split('\n');
  const starts = lineStartsOf(state.text);
  const flags = touchedFlags(lines.length, starts, state.selections);
  const inserted: number[] = [];
  for (let l = 0; l < lines.length; l++) {
    inserted.push(0);
    if (flags[l]) {
      const w = indentWidthOf(lines[l], tabSize);
      const k = tabSize - (w % tabSize);
      inserted[l] = k;
      lines[l] = ' '.repeat(k) + lines[l];
    }
  }
  const newText = lines.join('\n');
  const newStarts = lineStartsOf(newText);
  const selections: Selection[] = [];
  for (const sel of state.selections) {
    selections.push({
      anchor: mapPosIndent(sel.anchor, starts, newStarts, flags, inserted),
      caret: mapPosIndent(sel.caret, starts, newStarts, flags, inserted),
    });
  }
  return { text: newText, selections };
}

function refOutdentLines(state: EditorState, tabSize: number): EditorState {
  const lines = state.text.split('\n');
  const starts = lineStartsOf(state.text);
  const flags = touchedFlags(lines.length, starts, state.selections);
  const removed: number[] = [];
  for (let l = 0; l < lines.length; l++) {
    removed.push(0);
    if (flags[l]) {
      const k = outdentCountOf(lines[l], tabSize);
      removed[l] = k;
      lines[l] = lines[l].slice(k);
    }
  }
  const newText = lines.join('\n');
  const newStarts = lineStartsOf(newText);
  const selections: Selection[] = [];
  for (const sel of state.selections) {
    selections.push({
      anchor: mapPosOutdent(sel.anchor, starts, newStarts, flags, removed),
      caret: mapPosOutdent(sel.caret, starts, newStarts, flags, removed),
    });
  }
  return { text: newText, selections };
}

function refInsertTab(state: EditorState, tabSize: number): EditorState {
  let allCarets = true;
  for (const sel of state.selections) {
    if (sel.anchor !== sel.caret) {
      allCarets = false;
    }
  }
  if (!allCarets) {
    return refIndentLines(state, tabSize);
  }
  const lines = state.text.split('\n');
  const starts = lineStartsOf(state.text);
  const perLine: CaretInsert[][] = [];
  for (let l = 0; l < lines.length; l++) {
    perLine.push([]);
  }
  const seen = new Set();
  for (const sel of state.selections) {
    if (seen.has(sel.caret)) {
      continue;
    }
    seen.add(sel.caret);
    const loc = locate(starts, sel.caret);
    const col = visualWidthOf(lines[loc.line].slice(0, loc.col), tabSize);
    perLine[loc.line].push({ col: loc.col, spaces: tabSize - (col % tabSize) });
  }
  for (let l = 0; l < lines.length; l++) {
    const inserts = perLine[l];
    inserts.sort((a, b) => b.col - a.col);
    for (const ins of inserts) {
      lines[l] = lines[l].slice(0, ins.col) + ' '.repeat(ins.spaces) + lines[l].slice(ins.col);
    }
  }
  const newText = lines.join('\n');
  const newStarts = lineStartsOf(newText);
  const selections: Selection[] = [];
  for (const sel of state.selections) {
    const loc = locate(starts, sel.caret);
    let shift = 0;
    for (const ins of perLine[loc.line]) {
      if (ins.col <= loc.col) {
        shift += ins.spaces;
      }
    }
    const pos = newStarts[loc.line] + loc.col + shift;
    selections.push({ anchor: pos, caret: pos });
  }
  return { text: newText, selections };
}

export { refInsertTab, refIndentLines, refOutdentLines };
