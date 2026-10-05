# sourcemap

Go library for composing and querying multi-layer source mappings in a
compile chain (transpiler → bundler → minifier → ...).

## Model

- All intervals are half-open `[Start, End)`; `Start == End` is a
  zero-width insertion point.
- A `Segment` maps one output interval to **zero, one, or many** input
  intervals:
  - 0 sources — **synthesized** fragment (generated code, no provenance);
  - 1 source — **copied** fragment;
  - ≥2 sources — concatenation of several source fragments, laid out over
    the output interval in order. Multi-source fragments are never
    collapsed into a fake contiguous original interval.
- A `Mapping` is one layer: sorted, non-overlapping segments. Unmapped
  positions are treated as synthesized during composition.

## API

- `New(segs)` / `Must(segs)` — validate and build a layer.
- `m.Query(pos, bias)` — segments owning `pos`; `Left`/`Right` bias decides
  ownership of boundary positions. Zero-width segments located exactly at
  `pos` always contribute.
- `m.SourcesAt(pos, bias)` — coarse provenance: sources of owning segments.
- `m.SourcePointsAt(pos, bias)` — exact per-point provenance as zero-width
  source spans.
- `Compose(outer, inner)` — fold two layers into one mapping final output
  directly to original source. Copied stays copied, synthesized stays
  synthesized; adjacent pieces are merged only when they provably came from
  one contiguous original interval.

## Verification

Tests check composed mappings against per-character source sets computed by
independently chaining point queries through each layer, covering
interleaved copied/synthesized fragments, multi-source segments, and
zero-width boundaries under both biases (`go test ./...`).
