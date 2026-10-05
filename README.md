# chunked-analyzer

Chunked binary-image connected-component analysis library (C++17).

Chunks of a binary image may arrive **in any order**; the library keeps only the
chunk currently being processed plus a compact **boundary summary** per received
chunk, yet produces results identical to a whole-image model:

- **Connected-component labeling** of foreground with 8-connectivity
  (background uses 4-connectivity for Jordan-consistent hole semantics).
- **Per-component statistics**: area, perimeter, hole count.
- **Stable final numbering**: component ids are 1-based, assigned in row-major
  order of each component's minimum pixel coordinate `(minY, minX)`.

## Semantics

- **Area** — number of foreground pixels in the component.
- **Perimeter** — 4-neighborhood boundary length, computed as `4·A − 2·E`
  (`E` = number of 4-adjacent foreground pixel pairs). It counts the outer
  boundary, hole boundaries, and edges lying on the image border.
- **Holes** — a hole is a 4-connected background region that never touches the
  image border. Ownership is decided by the *nesting tree*: foreground and
  background regions form an adjacency graph rooted at the virtual exterior
  (BFS in canonical min-pixel order), and a hole belongs to its parent — the
  foreground component that encloses it. This stays well-defined even for
  "bullseye" configurations where one background ring touches both an outer
  ring and an inner disk, and is independent of chunk arrival order.

## Memory model

For each chunk only four edge arrays are retained (`EdgePix{fg,bg}` per edge
pixel) plus one union-find node per local component. All pixel data of a chunk
is discarded after it is stitched to its already-arrived neighbors.

## API

```cpp
#include "chunked_analyzer.h"

chunked::ChunkedAnalyzer an(width, height, chunkW, chunkH);
an.addChunk(cx, cy, data);        // any order, each chunk exactly once
an.finalize();                    // after all chunks arrived
const auto& comps = an.components(); // sorted by (minY, minX), id == index+1
auto labels = an.labelChunk(cx, cy, data); // materialize final ids for a chunk
```

`data` is a row-major buffer of `chunkWidth(cx) * chunkHeight(cy)` bytes
(edge chunks are clipped); `0` = background, non-zero = foreground.

## Build & test

```sh
cmake -S . -B build && cmake --build build
ctest --test-dir build
```

Tests cover diagonal corner contact across chunks, four-chunk junctions,
all-background chunk borders, holes spanning chunk boundaries, nested
(bullseye) holes, structured patterns (checkerboard, stripes, full/empty),
and 1000 randomized images with random chunk sizes and arrival orders — all
cross-checked against an independent whole-image reference model, including
the full materialized label map.
