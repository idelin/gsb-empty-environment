# track-sizing

Fixed / content / proportional track size distribution in pure TypeScript,
with spanning-item minimum widths, gaps and overflow diagnostics. The node
tree and glyph metrics come from fixtures — no browser layout engine is
involved in any core computation.

## Requirements

- Node.js >= 23.6 (runs TypeScript natively via type stripping; zero
  dependencies, no build step)

## Run

```sh
npm test     # node --test test/   (Node.js built-in test runner)
npm run demo # node demo/run-sample.ts
```

## How it works

1. **Base sizes** — fixed tracks take their exact size; content tracks take
   the largest minimum width among items wholly inside them (text is
   measured with the fixture glyph table); proportional tracks start at 0.
2. **Span constraints (iterative, convergent)** — an item spanning N
   columns requires `sum(spanned tracks) + gap * (N-1) >= minWidth`.
   Deficits are shared equally by the growable spanned tracks (content and
   proportional; fixed tracks only if nothing else can grow). Base sizes
   only ever increase and are bounded, so the loop provably reaches a
   fixpoint; it stops as soon as a full pass changes nothing.
3. **Overflow** — if `sum(base) + gaps > containerWidth`, an `overflow`
   diagnostic reports the excess; track sizes stay non-negative.
4. **Free space** — otherwise the remaining space is split between
   proportional tracks by weight. If every weight is zero there is no
   division by zero: the leftover is reported as
   `undistributed-free-space`.

## Files

- `src/glyph-metrics.ts` — fixture glyph table + deterministic measuring
- `src/tree.ts` — node tree types and flattening into grid items
- `src/tracks.ts` — the track sizing engine
- `src/fixtures.ts` / `src/layout.ts` — fixture data and orchestration
- `test/tracks.test.ts` — node:test suite
- `demo/run-sample.ts` — runnable sample (normal, overflow, zero-weight)
