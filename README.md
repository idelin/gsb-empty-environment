# glyph-truncation-engine

Fixture-driven text truncation engine in TypeScript. The node tree and
glyph metrics are given as plain data; no browser layout engine is
involved in any core computation. Runs on Node.js (>= 22.6, developed
against v23.9) using the built-in type stripping and the built-in
`node:test` runner — zero dependencies.

## Run

```sh
node --test test/*.test.ts   # test suite (node:test)
node examples/demo.ts        # runnable showcase
```

## Capabilities

- **Multi-line clamp, middle ellipsis, key-fragment retention** — three
  truncation modes (`end` / `middle` / `key`) over greedy cluster-level
  line wrapping (`src/layout.ts`).
- **Grapheme clusters are atomic** — emoji ZWJ sequences, flags and
  combining-mark sequences are segmented with `Intl.Segmenter` and can
  never be split (`src/cluster.ts`).
- **Unbreakable entities** — entity nodes form atomic groups; they
  overflow whole rather than break, and cut positions never land inside
  them.
- **Mixed-direction fragments** — simplified bidi: strong-character
  direction detection, two/three embedding levels, UBA L2 visual
  reordering (`src/bidi.ts`). Selection direction (`backward`) is
  preserved for rtl text.
- **Links across the ellipsis** — a link elided in the middle yields
  two visible fragments sharing one `linkId`/`href` and pointing into
  the same original range.
- **Visual ↔ source mapping** — `hitTest` maps a visual position back
  to a source offset (mirrored for rtl clusters), `selectRange` returns
  visible ranges + highlight rects + the full copied text, and
  `accessibleName` is always the complete original text
  (`src/mapping.ts`).
- **Late font metrics** — metric tables are versioned; new tables
  trigger an incremental re-layout, stale versions are rejected, and
  out-of-order async deliveries cannot overwrite a newer layout
  (`src/engine.ts`).
- **Explicit degradation and conflicts** — an ellipsis wider than the
  line produces a `degraded` result with no ellipsis placed; required
  key fragments that mutually exceed capacity produce a `key-overflow`
  conflict with the offending `keyId`s.
- **Exhaustive optimality** — single-ellipsis placements are chosen by
  exhaustive search (`src/optimize.ts`); `test/exhaustive.test.ts`
  cross-checks the engine against an independent brute-force
  enumeration of every placement on hundreds of random small layouts.

## Layout of the repo

- `src/types.ts` — node tree, metric table, layout result types
- `src/cluster.ts` — grapheme segmentation
- `src/bidi.ts` — direction detection + visual reordering
- `src/flatten.ts` — node tree → cluster stream with source offsets
- `src/metrics.ts` — versioned metric tables, ellipsis measurement
- `src/layout.ts` — wrapping, truncation planner, visual lines, links
- `src/optimize.ts` — exhaustive placement search
- `src/mapping.ts` — hit test / selection / accessible name
- `src/engine.ts` — incremental engine with metric-version guards
- `fixtures/` — node trees and metric tables used by tests and demo
- `test/` — `node:test` suites
- `examples/demo.ts` — end-to-end showcase
