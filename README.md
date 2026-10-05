# sliding

Sliding event-time window quantiles over a mutable event set (Go).

## Semantics

- **Event set**: events are keyed by `ID`; `Upsert` inserts or revises,
  `Retract` removes. Revisions may move an event across windows.
- **Quantile**: for rational `q = num/den` in `[0, 1]`, the q-quantile of
  `n` values is the element at index `floor(q*(n-1))` after sorting.
- **Windows**: `(k*slide, size+k*slide]` for `k = 0, 1, ...` — left-open,
  right-closed. An event at exactly the window end is included; one at
  exactly the start is not.
- **Watermark**: a window's result is emitted once the watermark passes its
  end. `AdvanceWatermark` never lets the watermark retreat.
- **Late corrections**: a late revision/retraction touching a closed window
  yields `Retract(old)` followed by `Emit(new)`; if the window becomes
  empty, only the retraction is emitted; if a previously empty closed
  window gains data, only the emission is emitted. Events are retained
  indefinitely, so corrections may be arbitrarily late.
- **Empty windows** emit nothing.

## Example

```go
eng, _ := sliding.NewEngine(10, 10, sliding.MustFraction(1, 2)) // median
eng.Upsert(sliding.Event{ID: "a", Value: 1, Time: 5})
eng.Upsert(sliding.Event{ID: "b", Value: 2, Time: 6})
eng.Upsert(sliding.Event{ID: "c", Value: 3, Time: 7})
eng.AdvanceWatermark(10)                    // EMIT (0,10] 2
eng.Upsert(sliding.Event{ID: "a", Value: 100, Time: 5})
// RETRACT (0,10] 2 ; EMIT (0,10] 3
```

## Testing

`engine_test.go` contains unit tests for the quantile index, window
boundaries, empty windows, duplicate values, late corrections and watermark
monotonicity, plus `TestAgainstRebuildOracle`: a randomized test whose
oracle rebuilds the sorted set of every closed window from scratch after
each operation and diffs the resulting output stream against the
incremental engine.

Run with: `go test ./...`
