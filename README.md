# marker-overlay

A TypeScript marker library: it overlays half-open ranges contributed by
multiple analyzers and outputs minimal disjoint segments with their final
resolved styles.

## Model

Each analyzer contributes `Marker`s:

```ts
interface Marker {
  id: string;        // unique id; upsert with the same id = update
  source: string;    // analyzer id, arbitrates same-priority conflicts
  start: number;     // inclusive character offset
  end: number;       // exclusive character offset; start === end => anchor
  priority: number;  // higher wins
  attrs: Attrs;      // style attributes, e.g. { color: 'red' }
}
```

## Resolution semantics

- Ranges are **half-open** `[start, end)`; a marker covers offsets
  `start .. end - 1`.
- Attributes resolve **per key**: among the markers covering a character and
  defining key `k`, the winner is the greatest by `compareMarkers`:
  higher `priority` first, ties broken by lexicographically **greater
  `source` id**, then greater marker `id` (total, deterministic order).
- **Empty ranges are anchors**: they occupy no character, never split or
  restyle segments, and are reported separately via `anchors()`.
- `segments(from?, to?)` returns the **minimal disjoint** partition of the
  covered region (clipped to the window): adjacent elementary intervals whose
  resolved attributes are deep-equal are merged; gaps produce no segment.

## API

```ts
const set = new MarkerSet();
set.upsert(marker);          // insert or update (replace by id)
set.remove(id);              // delete by id
set.removeSource('lint');    // delete everything one analyzer contributed
set.segments();              // => Array<{ start, end, attrs }>
set.anchors();               // => Array<{ offset, markers }>
set.resolveAt(offset);       // resolved attrs at one character
```

## Verification

`test/` cross-checks the sweep-line output against an independent
**per-character attribute model** (`test/model.ts`), including:

- updates (upsert with an existing id) and deletions (`remove`);
- merging of adjacent segments with equal resolved styles;
- priority overlay, per-key independence, source-id arbitration;
- anchors occupying no characters;
- seeded randomized fuzz over interleaved upsert/remove, verified after
  every step, plus windowed (clipped) segment checks.

## Build & test

```sh
npm install   # typescript (devDependency)
npm test      # tsc -p tsconfig.json && node dist/test/run.js
```
