/**
 * Synthetic event-replay tests for the IME-aware editor state machine.
 *
 * Each test feeds a recorded event sequence (mirroring real browser
 * dispatch orders) into the controller and asserts the resulting state.
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { EditorController } from '../src/editor.ts';
import type { EditorEvent, InputType } from '../src/editor.ts';

function input(data: string | null, inputType: InputType = 'insertText', isComposing = false): EditorEvent {
  return { type: 'input', inputType, data, isComposing };
}

function replay(controller: EditorController, events: EditorEvent[]): void {
  for (const event of events) controller.dispatch(event);
}

/** Chrome order: compositionend fires BEFORE the final input event. */
function chromeCompositionEvents(text: string, steps: string[]): EditorEvent[] {
  const events: EditorEvent[] = [{ type: 'compositionstart' }];
  for (const step of steps) {
    events.push({ type: 'compositionupdate', data: step });
    events.push(input(step, 'insertCompositionText', true));
  }
  events.push({ type: 'compositionend', data: text });
  events.push(input(text, 'insertFromComposition', false)); // duplicate trailing input
  return events;
}

/** Firefox/Safari order: final composing input fires BEFORE compositionend. */
function firefoxCompositionEvents(text: string, steps: string[]): EditorEvent[] {
  const events: EditorEvent[] = [{ type: 'compositionstart' }];
  for (const step of steps) {
    events.push({ type: 'compositionupdate', data: step });
    events.push(input(step, 'insertCompositionText', true));
  }
  events.push({ type: 'compositionend', data: text });
  return events;
}

test('composition commits once despite duplicate trailing input (Chrome order)', () => {
  const editor = new EditorController();
  replay(editor, chromeCompositionEvents('你好', ['你', '你好']));
  const state = editor.getState();
  assert.equal(state.text, '你好');
  assert.equal(state.composing, false);
  assert.equal(state.undoDepth, 1);
});

test('composition commits once when final input precedes compositionend (Firefox order)', () => {
  const editor = new EditorController();
  replay(editor, firefoxCompositionEvents('你好', ['你', '你好']));
  assert.equal(editor.getState().text, '你好');
  assert.equal(editor.getState().undoDepth, 1);
});

test('genuine typing after commit is not mistaken for a duplicate', () => {
  const editor = new EditorController();
  replay(editor, chromeCompositionEvents('好', ['好']));
  // A real keystroke arrives as insertText, not insertFromComposition.
  replay(editor, [input('好', 'insertText')]);
  assert.equal(editor.getState().text, '好好');
});

test('no truncation and no validation while composing; cap applied at compositionend', () => {
  const validationCalls: string[] = [];
  const editor = new EditorController({
    maxLength: 5,
    validate: (text) => {
      validationCalls.push(text);
      return null;
    },
  });
  const steps = ['你', '你好', '你好吗', '你好吗朋', '你好吗朋友', '你好吗朋友们'];
  const events: EditorEvent[] = [{ type: 'compositionstart' }];
  for (const step of steps) {
    events.push({ type: 'compositionupdate', data: step });
    events.push(input(step, 'insertCompositionText', true));
  }
  replay(editor, events);

  // Mid-composition: 6 code points exceed the cap of 5, yet nothing is
  // truncated and validation has not run.
  assert.equal(editor.getState().text, '你好吗朋友们');
  assert.deepEqual(validationCalls, []);

  replay(editor, [{ type: 'compositionend', data: '你好吗朋友们呀' }]);
  const state = editor.getState();
  assert.equal(state.text, '你好吗朋友'); // truncated to 5 code points at commit
  assert.deepEqual(validationCalls, ['你好吗朋友']); // validated exactly once
});

test('length cap counts Unicode code points, not UTF-16 units', () => {
  const editor = new EditorController({ maxLength: 3 });
  // Each emoji is ONE code point but TWO UTF-16 code units.
  replay(editor, [input('😀😀😀😀', 'insertText')]);
  assert.equal(editor.getState().text, '😀😀😀');
});

test('composition-end truncation never splits a surrogate pair', () => {
  const editor = new EditorController({ maxLength: 2 });
  replay(editor, firefoxCompositionEvents('a😀b', ['a😀b']));
  const state = editor.getState();
  assert.equal(state.text, 'a😀');
  assert.equal([...state.text].length, 2);
});

test('composition cancel restores pre-composition state without undo entry or validation', () => {
  const validationCalls: string[] = [];
  const editor = new EditorController({
    validate: (text) => {
      validationCalls.push(text);
      return null;
    },
  });
  replay(editor, [input('ab', 'insertText')]);
  assert.equal(validationCalls.length, 1);
  const undoDepthBefore = editor.getState().undoDepth;

  replay(editor, [
    { type: 'compositionstart' },
    input('你', 'insertCompositionText', true),
    input('你好', 'insertCompositionText', true),
    { type: 'compositioncancel' },
  ]);
  const state = editor.getState();
  assert.equal(state.text, 'ab');
  assert.equal(state.focus, 2);
  assert.equal(state.undoDepth, undoDepthBefore);
  assert.equal(validationCalls.length, 1);
});

test('compositionend with empty data is treated as cancel', () => {
  const editor = new EditorController();
  replay(editor, [input('ab', 'insertText')]);
  replay(editor, [
    { type: 'compositionstart' },
    input('你', 'insertCompositionText', true),
    { type: 'compositionend', data: '' },
  ]);
  assert.equal(editor.getState().text, 'ab');
  assert.equal(editor.getState().undoDepth, 1);
});

test('selection replacement: plain input replaces the selected range', () => {
  const editor = new EditorController();
  replay(editor, [
    input('hello world', 'insertText'),
    { type: 'selectionchange', anchor: 6, focus: 11 },
    input('there', 'insertText'),
  ]);
  const state = editor.getState();
  assert.equal(state.text, 'hello there');
  assert.equal(state.focus, 11);
});

test('selection replacement: composition replaces the selected range', () => {
  const editor = new EditorController();
  replay(editor, [
    input('hello world', 'insertText'),
    { type: 'selectionchange', anchor: 0, focus: 5 },
    ...firefoxCompositionEvents('你好', ['你', '你好']),
  ]);
  assert.equal(editor.getState().text, '你好 world');
});

test('undo restores the exact pre-composition state; redo reapplies the commit', () => {
  const editor = new EditorController();
  replay(editor, [input('ab', 'insertText')]);
  replay(editor, [
    { type: 'selectionchange', anchor: 1, focus: 1 },
    ...chromeCompositionEvents('你好', ['你', '你好']),
  ]);
  assert.equal(editor.getState().text, 'a你好b');

  editor.undo();
  let state = editor.getState();
  assert.equal(state.text, 'ab');
  assert.equal(state.focus, 1); // caret back where composition started

  editor.redo();
  state = editor.getState();
  assert.equal(state.text, 'a你好b');
  assert.equal(state.focus, 3);
});

test('undo of a cancelled composition is a no-op for the composition itself', () => {
  const editor = new EditorController();
  replay(editor, [input('x', 'insertText')]);
  replay(editor, [
    { type: 'compositionstart' },
    input('你', 'insertCompositionText', true),
    { type: 'compositioncancel' },
  ]);
  editor.undo(); // undoes the 'x' insert, not the cancelled composition
  assert.equal(editor.getState().text, '');
});

test('validation commits on plain input and on composition end only', () => {
  const seen: string[] = [];
  const editor = new EditorController({
    validate: (text) => {
      seen.push(text);
      return text.includes('!') ? 'no exclamation marks' : null;
    },
  });
  replay(editor, [input('ok', 'insertText')]);
  replay(editor, chromeCompositionEvents('!', ['!']));
  assert.deepEqual(seen, ['ok', 'ok!']);
  assert.equal(editor.getState().validationError, 'no exclamation marks');
});
