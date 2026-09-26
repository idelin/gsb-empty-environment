# spill-tile-convolver

Spillable tile-based convolver for multi-channel (RGBA float32) rasters that
exceed the memory budget. Pure TypeScript on Node.js (>= 22.6, type-stripping
mode) — no Canvas filters, no image-processing libraries, no dependencies.

## Features

- **Chained separable kernels**: any number of stages, each with its own
  horizontal/vertical 1D kernels, arbitrary anchors (odd/even lengths,
  asymmetric taps) and per-stage boundary policies
  (`clamp`, `zero`, `wrap`, `mirror`, `reflect101`).
- **Exact halo derivation**: fused mode computes per-stage input regions
  backwards from each output tile; `wrap` widens to the full dimension and
  mirror policies extend coverage to reflected coordinates. Tile edges are
  never treated as image edges — cross-tile samples come from neighbour
  tiles, boundary policies apply only at image edges.
- **Spillable tile store**: LRU cache with disk spill; all buffers are
  accounted in a `MemTracker` so peak memory is exact and budget-capped.
- **Budget guard**: `minimumBudgetBytes()` derives the exact minimum working
  set (worst-tile halo regions + one input-region cache); anything smaller is
  rejected with `InsufficientBudgetError` before any work starts.
- **Kernel hot-swap**: chains are immutable and versioned. A run snapshots
  its chain, so a retry after a failure never mixes old/new kernel versions.
  Stale tiles are recomputed locally (`ConvolverSession.recomputeStale`).
- **Local invalidation**: editing one source tile invalidates exactly the
  output tiles inside the total chain halo (two-layer propagation for a
  two-stage chain) via `ConvolverSession.affectedTiles` / `recomputeTiles`.
- **Cancellation & cleanup**: aborting or failing a run releases the cache
  and deletes every spill/temp/partial file; nothing is published.
- **Atomic publish**: output tiles are written first, then the root index is
  published by temp-file + rename.
- **Metrics**: every run reports multiply-accumulate ops and peak memory.

## Usage

```bash
npm test        # node --test test/*.test.ts
npm run example # builds an RGBA image, runs fused + stepwise, prints stats
```

```ts
import { kernel, stage, makeChain, runPipeline, minimumBudgetBytes } from './src/index.ts';

const chain = makeChain(1, [
  stage(kernel([0.25, 0.5, 0.25]), kernel([0.25, 0.5, 0.25]), 'mirror', 'mirror'),
  stage(kernel([-0.1, 1.25, -0.1, 0.05], 1), kernel([1], 0), 'clamp', 'clamp'),
]);
const min = minimumBudgetBytes(w, h, 16, chain, 'fused');
const result = runPipeline({
  source, chain, tileSize: 16, mode: 'fused',
  budgetBytes: min.totalBytes, workDir: 'out/',
});
console.log(result.ops, result.peakMemoryBytes, result.indexPath);
```

## Layout

- `src/boundary.ts` — closed-form boundary index mapping (no loops, width-1 safe)
- `src/kernels.ts` — kernel/chain types, halo math, `requiredInputRect`
- `src/stage.ts` — boundary-aware sampler + single-stage region convolution
- `src/tiles.ts` — tile grid, LRU spill store, tiled raster
- `src/pipeline.ts` — budget derivation, fused/stepwise execution, atomic publish
- `src/session.ts` — hot-swap, invalidation, version-safe retries
- `src/reference.ts` — untiled per-pixel reference used as ground truth
