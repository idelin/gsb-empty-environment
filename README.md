# scroll-chain-model

Layered scroll model in TypeScript.

## Model

- `ScrollChain` holds layers ordered innermost-first; a displacement is
  offered to the innermost layer and only the out-of-bounds remainder is
  handed to each ancestor (`ScrollChain.scroll`).
- Each `ScrollLayer` clamps the offered delta into `[min, max]`, consumes
  what fits, and returns the overflow. `blocksChain: true`
  (overscroll-behavior: contain) swallows the overflow instead of
  propagating it -- a zero-range layer (`min === max`) still blocks.
- `ScrollChain.applyZoom(factor, anchor)` first computes the
  anchor-preserving offset `(offset + anchor) * factor - anchor` for every
  layer, then applies the resulting deltas innermost-first, so clamp
  overflow propagates upward exactly like a scroll.
- `setBounds` supports dynamic bounds (content/viewport resize) and
  re-clamps the current offset.

## Conservation invariant

Per layer: `offered === consumed + remainder + dropped`, with at most one
of `remainder`/`dropped` non-zero. Per chain: the input delta equals the
sum of consumed + dropped across layers plus the remainder escaping the
root. The test suite checks this on every operation, covering positive and
negative deltas, fractional pixels, dynamic bounds, and zoom.

## Commands

```sh
npm run build   # compile to dist/
npm test        # build + run conservation tests
```
