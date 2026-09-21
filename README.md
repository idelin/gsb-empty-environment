# Exact Sliding-Window Quantiles

Python 3.11, standard library only.

```python
from exact_quantile_window import ExactQuantileWindow

window = ExactQuantileWindow(window_size=10)
window.ingest("sensor-a", 1, timestamp=1, value=1)
window.ingest("sensor-a", 2, timestamp=2, value=1)
window.ingest("sensor-a", 3, timestamp=3, value=9)
window.ingest("sensor-a", 4, timestamp=4, value=20)

window.advance_watermark(10)  # interval is (0, 10]
window.quantile(0.5)          # 1
window.retract("sensor-a", 1)
window.quantile(0.5)          # 9
```

## Semantics

- Window interval is left-open and right-closed: `(watermark - width, watermark]`.
- The q-quantile is the sorted value at index `floor(q * (n - 1))`.
- An empty active window returns `None`.
- Repeated values remain separate events and each occupies one order-statistic position.
- Event identity is the hashable pair `(source, seq)`.
- Repeated ingestion of an identity is ignored; retraction removes only that identity.
- Expired and retracted identities remain in the identity record so duplicates and
  repeated retractions remain detectable.
- A timestamp at or before the current left edge is rejected as a late event.

## Maintenance Structure

`ExactQuantileWindow` uses:

- Identity dictionary: one record per distinct `(source, seq)` ever ingested, `O(U)`.
- Active min-heap: one physical tuple per active event and lazily removed active
  retraction, `O(A + R_a)`.
- Waiting min-heap: one physical tuple per future event and lazily removed waiting
  retraction, `O(W + R_w)`.
- Randomized Treap order-statistic tree: one node per distinct active value,
  `O(D)`; each node stores both the value's event multiplicity and subtree size.

Total retained space is therefore `O(U + D + A + R_a + W + R_w)`. Since duplicate
detection and historical retraction require recognizing every identity, `U` records
all identities ingested during the component's lifetime. Treap nodes are removed as
the last active event for a value expires or is retracted.

`memory_summary()` returns identity counts, Treap node count, physical heap entry
counts, and event-state counts.

## Costs

Let `n` be active events and `D` the number of distinct active values.

- Ingest: expected `O(log n)` for an active event; `O(log W)` for a future event.
- Active retraction: expected `O(log D)`; waiting/expired retraction is dictionary
  and status work after `O(1)` lookup.
- Exact quantile: expected `O(log D)`.
- Watermark advancement with no boundary event: `O(1)` heap-front work.

For a batch that pops `h` expired heap entries, really expires `k` active events
spread across `d <= k` distinct values, and activates `a` waiting events, cost is

```text
O(h log H + d log D + a log D)
```

and is worst-case `O((h + a) log U)` for that advancement. Equal expiring values are
combined before Treap deletion. Every heap tuple is pushed and popped at most once,
so over a monotonically advancing watermark the heap work is amortized rather than a
full re-sort of the window.

The Treap's randomized priorities affect only tree balance; order statistics are
computed from exact counts and subtree sizes. No approximate sketch is used.

## Oracle And Tests

`naive_oracle.NaiveQuantileWindow` intentionally scans the identity map and sorts
active values for every query. The test suite compares it against the Treap window
over randomized ingest, advance, retract, and duplicate-operation sequences.

```bash
python3 -m unittest discover -s tests -v
```
