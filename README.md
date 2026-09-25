# merged-grid

Merged-cell grid engine in TypeScript: 2D navigation, range selection and
clipboard copy. All core computation (text measurement, layout, hit-testing)
runs on fixture data — a node tree plus glyph metrics — with no browser
layout engine involved. Tested with the Node.js built-in test runner.

Requires Node.js >= 22.6 (runs `.ts` directly via type stripping;
developed on v23.9.0). No dependencies to install.

## Commands

```sh
npm test    # node --test test/*.test.ts
npm start   # node examples/demo.ts
```

## Model

- A **merge region** is an anchor cell plus a row/col span. The anchor
  holds the value; every other cell in the region is **covered**.
- The active cell is always a plain cell or an anchor — navigation jumps
  never land on a covered cell.

## Behavior

**Navigation** (`src/navigation.ts`)
- Moving into a region lands on its anchor.
- Leaving a region skips its whole span in one step.
- Moves that would leave the grid keep the current cell.

**Selection** (`src/selection.ts`)
- A drag is normalized to its bounding rect, then expanded so every
  merge it touches is fully covered.
- Expansion is monotone inside a finite grid, so iterating to a fixed
  point always terminates — including chains of merges where covering
  one region wakes the next.

**Copy** (`src/clipboard.ts`)
- The expanded rect is exported as a matrix: anchors contribute their
  value once, covered cells contribute an empty placeholder, so the
  rectangular structure is preserved. `toTSV` serializes for the
  clipboard.

**Layout** (`src/layout.ts`, `src/fixtures/`)
- Column widths and row offsets are computed from the node-tree fixture
  and glyph-metrics fixture (`measureText`), replacing the browser
  layout engine; `hitTest` maps pixels back to cells.

## Sample run (`npm start`)

```
== Layout (fixture node tree + glyph metrics, no browser) ==
rows=4 cols=4
columnOffsets=[0, 61, 129, 141, 190]
rowOffsets=[0, 28, 56, 84, 112]
hitTest(30, 5) -> A1

== Navigation (jumps never land on covered cells) ==
start at A1
Arrow right -> B1
Arrow right -> D1
Arrow down  -> D2
Arrow down  -> D3
Arrow left  -> B2
Arrow left  -> A2
Arrow up    -> A1

== Selection (touching a merge covers the whole region) ==
drag A1..B2 -> {"top":0,"left":0,"bottom":2,"right":2} (B1:C1 and B2:C3 pulled in)

== Chained merges (expansion runs to a fixed point) ==
drag B3..B4 -> {"top":1,"left":0,"bottom":3,"right":2} (B2:C3 then A4:C4)

== Copy (rectangular, empty placeholders for covered cells) ==
["Quarter","Revenue","","Delta"]
["Q1","120.5","","4.2%"]
["Q2","","","-1.3%"]
["Total","","","3.1%"]
```

## Test results (`npm test`)

22 tests, 22 pass, 0 fail — covering merge indexing, navigation invariants
(exhaustive: no move from any cell ever lands on a covered cell),
fixed-point selection expansion over merge chains, rectangular copy with
empty placeholders, and fixture-driven layout/hit-testing.
