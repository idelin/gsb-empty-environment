# snap-drag

TypeScript drag-snapping engine: pointer positions snap to the nearest
reference line, with hysteresis to keep the snap stable.

## Model

- **Input coordinates** are integer subpixels; `scale` subpixels make one
  logical pixel (e.g. `scale: 2` for a 2x canvas). Reference lines and
  thresholds live in logical-pixel space, so lines may sit at fractional
  positions while the pointer stays on the integer subpixel grid.
- **Vertical lines snap `x`, horizontal lines snap `y`**; the two axes
  are evaluated independently and can be engaged at the same time.

## Snapping rules

1. A free axis engages the nearest line within `snapThreshold`
   (logical pixels). Ties on distance resolve to the smallest line id.
2. An engaged axis holds its line while the distance stays within the
   larger `releaseThreshold` — the hysteresis band between the two
   thresholds prevents flicker when the pointer jitters at the edge.
   The hold is sticky: a merely-closer line does not steal an engagement.
3. `removeLine` on the engaged line releases that axis immediately;
   re-adding a line with the same id does not revive the old engagement.
4. `moveLine` keeps the engagement, so the snapped output tracks the
   line until the gap exceeds `releaseThreshold`.

## Usage

```ts
import { SnapEngine } from './src/snap.ts';

const engine = new SnapEngine({ snapThreshold: 5, releaseThreshold: 8, scale: 2 });
engine.upsertLine({ id: 1, orientation: 'vertical', position: 100 });
engine.upsertLine({ id: 2, orientation: 'horizontal', position: 50 });

// On every pointer move, feed integer subpixel coordinates:
const result = engine.snap({ x: 203, y: 99 });
// result.x / result.y   — snapped (or raw) integer subpixels
// result.xSnap / ySnap  — engaged line id, snapped value, distance, or null
```

## Tests

```sh
npm test   # node --test, runs TypeScript directly (Node >= 23.6)
```

The suite drives continuous position trajectories (inclusive integer
sweeps) through the engine to pin down the exact engage/release
boundaries, tie-breaking, line deletion and movement, scale conversion,
and dual-axis independence.
