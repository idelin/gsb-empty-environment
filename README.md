# Paged compressed DFA transition tables

A page-oriented, compressed transition table for large deterministic finite
automata, implemented in Python 3.11 with **only the standard library** (no
regex or automaton package anywhere). The same alphabet and transition
structure is fed in explicitly; encoding, patching and verification all work
from that explicit model.

## Compression model

A full `N_states × N_symbols` matrix is split into pages of a caller-supplied
`page_size` symbols. Every page chunk is encoded independently as:

- one **default target** for the page;
- a sorted list of half-open **exception intervals** `[start, end, target)`;
- an optional **explicit-cell bitmap** ("sticky" cells).

Three mechanisms are combined:

1. **Default target** — cells equal to the implicit default need no interval.
   The encoder chooses the most frequent *implicit* target; explicitly
   declared cells never vote.
2. **Whole-row sharing** — rows whose page contents are identical share the
   same blobs (content addressed by hash). Directory entries point at a
   canonical owning row.
3. **Interval exceptions** — adjacent exceptional cells with the same target
   coalesce into one interval; intervals never cross page boundaries.

The explicit-cell bitmap is the key invariant: an explicit transition
(including an explicit transition into the dead state) is always stored, even
when its value equals the default. Changing a row default therefore cannot
hide an explicit dead transition.

## Storage, versions and queries

`pagedfa/container.py` defines an append-only single file: a 64-byte header,
CRC32-framed blobs and an 8-byte active-version-root pointer. Publishing a new
version appends a new version-root blob and overwrites that pointer
atomically. Old views keep referencing the old root offset through their own
read-only `mmap`, so **hot replacement never invalidates outstanding views**.

Opening a `TableView` validates, before any query is possible:

- header, version-root CRC and root JSON;
- every directory `(state, page)` index against the declared grid;
- every blob offset framing/CRC and shared-row owner;
- the start state, accepting states and (on first page load) default and
  interval targets against the state range.

Pages themselves are validated lazily on first read. `view.verify_pages()`
forces eager validation. `view.stats.snapshot()` reports actual page loads,
cache hits and distinct pages touched — queries never decompress the whole
table. A custom `CallbackProvider` models remote/on-demand fetching.

## Reversible patches

`pagedfa/patching.py` supports two edit kinds per page:

- `CellEdit(pos, target, explicit)` — overwrite one cell;
- `DefaultEdit(new_default)` — change the page default (explicit cells kept).

A `PageDelta` records the exact base blob offset; applying it decodes only
that page, emits a fresh blob (copy-on-write: shared rows detach instead of
mutating), appends the delta for audit and publishes a new version. The
inverse delta restores the default, cells, explicit bits, accepting set and
start state byte-for-byte.

## Error taxonomy

| Condition | Exception | When |
| --- | --- | --- |
| state/symbol index out of range | `InvalidIndexError` | open validation / query |
| target outside state range | `BadTargetError` | input / page decode |
| root or directory offset outside file | `CorruptOffsetError` | open |
| blob payload CRC mismatch | `BadChecksumError` | page read |
| requested page absent | `MissingPageError` | page read |
| provider aborts a load | `LoadCancelledError` | page read (retryable) |
| delta base offset mismatch | `PatchError` | patch time |

Cross-page symbol ranges are impossible: symbol ids map to `(page, local_pos)`
with a hard range check, and pages encode disjoint symbol windows.

## Layout

- `pagedfa/dfa.py` — explicit `Alphabet`/`DFA`, product-BFS language equivalence.
- `pagedfa/chunks.py` — per-page chunk codec.
- `pagedfa/encoder.py` — default selection, row sharing, page plans, report.
- `pagedfa/container.py` — file format, CRC blobs, mmap, atomic root publish.
- `pagedfa/store.py` — `TableView` (read-only queries), `TableWriter`.
- `pagedfa/patching.py` — edits, reversible deltas, copy-on-write apply.
- `examples/demo.py` — self-contained counterexamples, runnable individually.
- `tests/` — unittest suites (20 tests).

## Running

```sh
python3.11 -m unittest discover -s tests -v
python3.11 examples/demo.py                 # all scenarios
python3.11 examples/demo.py hotswap stats   # selected scenarios
```
