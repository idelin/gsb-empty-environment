# tab-stop-editor

Multi-selection indent/unindent library for code editors, with fixed
4-column tab stops and byte-exact undo.

## Semantics

- **Offsets** are UTF-16 code-unit indices into the document string.
  Lines are delimited by `"\n"`; a `"\r"` is ordinary content.
- **Shared lines**: lines touched by several selections are deduplicated
  and indented exactly once (`touchedLines` in `src/indent.ts`).
- **Touched lines**: a selection touches its start line through its end
  line; a non-empty selection ending exactly at a line start does not
  touch that final line.
- **Indent** (`Tab`): inserts whitespace at the start of every touched
  non-empty line, advancing to the next 4-column tab stop (column 0, so
  one full stop: `"    "` or `"\t"` with `useTabs`).
- **Unindent** (`Shift+Tab`): removes at most one tab stop of leading
  whitespace per touched line. Existing tabs count by visual column; the
  line always lands exactly on the previous tab stop, padding with
  spaces when a removed tab overshoots (e.g. `"\t x"` → `"    x"`).
- **Selection mapping**: positions at a pure insertion point ride
  forward with the inserted text; positions inside removed whitespace
  clamp to the line start; later positions shift by the length delta.
- **Undo**: every operation records inverse edits (in edited-text
  coordinates, see `invertEdits` in `src/edits.ts`); `Editor.undo()`
  restores the previous text byte-for-byte and the selections exactly.

## Layout

- `src/visual.ts` — tab-stop / visual-column math (the shared spec).
- `src/edits.ts` — edit application, inversion, position mapping.
- `src/indent.ts` — main offset-based indent/unindent implementation.
- `src/reference.ts` — independent line-by-line implementation used to
  cross-check the main one.
- `src/editor.ts` — `Editor` class: text + selections + undo stack.
- `test/` — unit tests plus randomized differential tests
  (main vs. reference, overlapping selections, mixed tabs/spaces,
  byte-exact undo fuzzing).

## Test

Requires Node >= 22.6 (runs TypeScript via type stripping):

```sh
npm test
```
