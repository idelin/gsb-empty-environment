# IME-aware edit-control state machine

DOM-free TypeScript state machine for an edit control that consumes
`composition`, `input` and `selection` events. Built for correct IME
(input method editor) behavior.

## Guarantees

- **No truncation, no validation during composition.** While a composition
  session is active the text may temporarily exceed `maxLength`; the cap is
  applied uniformly once, at `compositionend`.
- **Code-point counting.** `maxLength` and all selection offsets count
  Unicode code points (`[...text]`), so surrogate pairs (emoji, rare CJK)
  are never split.
- **Composition-scoped undo.** A whole composition session produces exactly
  one undo entry holding the pre-composition snapshot; `undo()` restores
  text and selection from before `compositionstart`.
- **Duplicate trailing input swallowed.** Chrome dispatches
  `compositionend` *before* the final `input` (`insertFromComposition`,
  same data). That replayed event is detected and ignored so committed
  text is never inserted twice. Genuine re-typing arrives as `insertText`
  and is applied normally.
- **Cancel reverts cleanly.** `compositioncancel` (or `compositionend`
  with empty data) restores the pre-composition snapshot without pushing
  an undo entry or committing validation.
- **Selection replacement.** The next input — plain or composed — replaces
  the currently selected range.

## Event contract

```ts
type EditorEvent =
  | { type: 'compositionstart' }
  | { type: 'compositionupdate'; data: string }
  | { type: 'compositionend'; data: string }   // data === '' => cancel
  | { type: 'compositioncancel' }
  | { type: 'input'; inputType: InputType; data: string | null; isComposing: boolean }
  | { type: 'selectionchange'; anchor: number; focus: number }; // code-point offsets
```

## Usage

```ts
import { EditorController } from './src/editor.ts';

const editor = new EditorController({
  maxLength: 5,                          // Unicode code points
  validate: (text) => (text.includes('!') ? 'no !' : null),
});

editor.dispatch({ type: 'compositionstart' });
editor.dispatch({ type: 'input', inputType: 'insertCompositionText', data: '你好', isComposing: true });
editor.dispatch({ type: 'compositionend', data: '你好' });
editor.getState(); // { text: '你好', composing: false, undoDepth: 1, ... }
editor.undo();     // back to the pre-composition state
```

## Tests

Synthetic event-replay tests (Chrome and Firefox/Safari dispatch orders,
duplicate trailing input, cancel, selection replacement, code-point caps,
undo/redo) run with the Node built-in test runner — no dependencies, no
build step. Requires Node >= 22.6 (native TypeScript type stripping).

```sh
npm test
```
