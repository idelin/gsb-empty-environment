# merged-grid

Merged-cell grid in TypeScript: 2D navigation, range selection and copy.
The node tree and glyph metrics come from JSON fixtures — all core
computation (placement, column widths, row heights, hit resolution) is done
in this repo, not by a browser layout engine.

## Requirements

- Node.js >= 22.6 (developed and tested on v23.9.0, which runs TypeScript
  directly via type stripping — no build step, no dependencies).

## Layout of the repo

- `fixtures/table.json` — node tree (rows/cells with `colSpan`/`rowSpan`).
- `fixtures/glyphs.json` — glyph metrics (per-char advances, fallback,
  line height, cell padding).
- `src/glyphs.ts` — text measurement from the metrics fixture.
- `src/grid.ts` — HTML-table-style auto-placement, column widths (spanned
  cells push their deficit onto the last spanned column), row heights, and
  the `Grid` model with anchor/owner lookup.
- `src/navigation.ts` — `move(grid, anchor, dir)`: steps clear the current
  merged region, and the landing slot resolves to its region anchor, so a
  jump never lands on a covered slot. Edges keep the cursor in place.
- `src/selection.ts` — range selection normalized to a rectangle, then
  expanded to a fixed point: any merged region intersecting the selection
  is fully pulled in. Each round strictly grows the rectangle inside a
  bounded grid, so chained merges (A clips B, B clips C, ...) terminate.
- `src/clipboard.ts` — `copyRange` emits TSV preserving the rectangular
  structure; covered slots and holes become empty placeholder fields.
- `src/demo.ts` — runnable sample (`npm run demo`).
- `test/*.test.ts` — Node built-in test runner (`npm test`).

## Run

```sh
npm run demo   # node src/demo.ts
npm test       # node --test
```

## Sample run (`npm run demo`)

```
=== Layout (computed from fixtures, no browser) ===
grid: 4 rows x 4 cols
column widths: [25, 33, 28, 35]
row heights:   [14, 14, 14, 14]
  Alpha    anchor=(0,0) span=1x2 rect=(x:0, y:0, w:58, h:14)
  Beta     anchor=(0,2) span=1x1 rect=(x:58, y:0, w:28, h:14)
  Gamma    anchor=(0,3) span=2x1 rect=(x:86, y:0, w:35, h:28)
  Delta    anchor=(1,0) span=1x1 rect=(x:0, y:14, w:25, h:14)
  Epsilon  anchor=(1,1) span=1x1 rect=(x:25, y:14, w:33, h:14)
  Zeta     anchor=(1,2) span=1x1 rect=(x:58, y:14, w:28, h:14)
  Eta      anchor=(2,0) span=2x2 rect=(x:0, y:28, w:58, h:28)
  Theta    anchor=(2,2) span=1x1 rect=(x:58, y:28, w:28, h:14)
  Iota     anchor=(2,3) span=1x1 rect=(x:86, y:28, w:35, h:14)
  Kappa    anchor=(3,2) span=1x2 rect=(x:58, y:42, w:63, h:14)

=== 2D navigation (jumps never land on covered slots) ===
start at (1,0) (Delta)
  right -> (1,1) (Epsilon)
  right -> (1,2) (Zeta)
  right -> (0,3) (Gamma)
  up    -> (0,3) (Gamma) (edge: stay)
  down  -> (2,3) (Iota)
  down  -> (3,2) (Kappa)
  left  -> (2,0) (Eta)
  down  -> (2,0) (Eta) (edge: stay)

=== Range selection expands to a fixed point over merges ===
raw rect:      rows 1..2, cols 1..2
fixed point:   rows 0..3, cols 0..3  (chain: Eta -> Kappa -> Gamma)

=== Copy (rectangular, empty placeholders for covered slots) ===
  | Alpha		Beta	Gamma
  | Delta	Epsilon	Zeta	
  | Eta		Theta	Iota
  | 		Kappa	
```

## Test results (`npm test`, Node v23.9.0)

```
✔ copying a covered slot yields its anchor text plus an empty placeholder
✔ copy preserves rectangular structure with empty placeholders
✔ copy of a merge-free range is a plain TSV block
✔ copy range grows with the selection before placeholders are emitted
✔ row count and column count of the copy match the rectangle
✔ glyph measurement uses fixture advances with default fallback
✔ placement follows HTML table auto-placement with spans
✔ column widths and row heights derive from glyph metrics
✔ merged cell pixel rects union their spanned slots
✔ spanned cell wider than its columns widens the last spanned column
✔ covered slots are reported, anchors are not
✔ plain moves between adjacent anchors
✔ moving out of a merged cell clears its whole span
✔ landing on a covered slot resolves to the region anchor
✔ grid edges keep the cursor in place
✔ every reachable landing is an anchor, never a covered slot
✔ a long walk stays consistent
✔ touching a merged region selects the whole region
✔ partial overlap with a 2x2 merge pulls in the whole block
✔ chained merges expand to a fixed point and terminate
✔ selections clear of merges stay untouched
✔ expansion is idempotent (it is a fixed point)
✔ reversed drag normalizes before expanding
✔ out-of-range endpoints are clamped to grid bounds
✔ normalizeRect orders corners

ℹ tests 25
ℹ pass 25
ℹ fail 0
```
