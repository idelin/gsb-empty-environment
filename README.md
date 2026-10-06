# drag-snap

Drag snapping to guide lines for TypeScript, with hysteresis, integer subpixel
input and zoom-aware thresholds. Zero dependencies.

## Requirements

Node.js >= 23.6 (or >= 22.6 with `--experimental-strip-types`) to run the TS
sources and tests directly. See `.nvmrc`.

```sh
npm test
```

## Model

- **Coordinates are integer subpixels.** All inputs (`update(x, y)`, line
  positions) are integers; `subpixelsPerPixel` (default 64) defines how many
  subpixels make one logical pixel. Outputs are always integers — a snapped
  coordinate is exactly the line position.
- **Thresholds are screen pixels.** A drag point is `d` screen px from a line
  when `|drag - line| / subpixelsPerPixel * zoom = d`. Zooming in therefore
  shrinks the logical snap range; zooming out widens it.
- **Vertical lines snap `x`, horizontal lines snap `y`** — both axes are
  resolved independently in a single `update()`, so a point can snap
  horizontally and vertically at the same time.

## Snap state machine (per axis)

1. **Unsnapped** — snap to the nearest line within `snapThresholdPx`
   (inclusive). Equal distances are broken by ascending line id (numeric for
   number ids, lexicographic otherwise).
2. **Snapped** — stay snapped to the same line while within
   `releaseThresholdPx` (>= `snapThresholdPx`). The larger release band
   prevents jitter when hovering near the snap boundary. The snap follows the
   line if it moves (and releases if the line moves beyond the release band).
3. **Line removed** — `removeLine()` releases the snap synchronously; the next
   `update()` re-evaluates from the unsnapped state and may immediately snap
   to another nearby line.

Changing zoom via `setZoom()` re-evaluates the active snap on the next
`update()`: if the snapped distance now exceeds the release threshold, the
snap is dropped.

## API

```ts
import { Snapper } from 'drag-snap';

const snapper = new Snapper({
  snapThresholdPx: 4,     // snap when within 4 screen px
  releaseThresholdPx: 8,  // release when beyond 8 screen px
  subpixelsPerPixel: 64,  // optional, default 64
  zoom: 1,                // optional, default 1
});

snapper.addLine({ id: 'col-1', axis: 'vertical', position: 6400 });   // x = 100px
snapper.addLine({ id: 'row-1', axis: 'horizontal', position: 12800 }); // y = 200px

const { x, y } = snapper.update(6592, 12608); // drag at (103, 197)
// x -> { position: 6400,  snapped: true, lineId: 'col-1' }
// y -> { position: 12800, snapped: true, lineId: 'row-1' }

snapper.moveLine('col-1', 6528);
snapper.removeLine('row-1'); // releases the y snap immediately
snapper.setZoom(2);
```

## Tests

`tests/snapper.test.ts` sweeps continuous one-subpixel trajectories across the
snap/release boundaries and covers tie-breaking, simultaneous axis snapping,
line movement/deletion, zoom rescaling and the integer subpixel contract.
