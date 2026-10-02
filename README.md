# slidinghist

Sliding-window histogram with streaming P90/P99 estimation (Go, stdlib only).

## Design

- **Hybrid buckets**: fixed-width buckets (`0.01ms`) cover `[0, 10ms)`;
  logarithmic buckets (growth `1.004`) cover the tail up to `60s`, plus one
  overflow bucket. ~3.2k counters total.
- **No raw samples retained**: a ring buffer stores only one `uint32` bucket
  index per in-window sample (4KB for a 1000-sample window).
- **Reverse compensation**: when the window slides, the evicted sample's
  bucket counter is decremented — O(1) add and evict.
- **Theoretical error bound**: quantile estimates use each bucket's
  representative value (geometric midpoint in the log region), so the
  relative error is bounded by `(LogGrowth-1)/2 = 0.2%` in the log region —
  inside the required 0.5%.

## Usage

```go
w, _ := slidinghist.NewWindow(1000, slidinghist.DefaultConfig())
w.Add(12.3) // latency in ms
p90, p99 := w.P90(), w.P99()
```

## Run

```sh
go test -v ./...   # acceptance tests
go run ./cmd/demo  # streaming demo: spike storm enters, then slides out
```

## Test results (go1.24.2 darwin/arm64)

```
=== RUN   TestBucketLayoutBounds
--- PASS: TestBucketLayoutBounds (0.01s)
=== RUN   TestWindowExpiryAndReverseCompensation
--- PASS: TestWindowExpiryAndReverseCompensation (0.00s)
=== RUN   TestQuantileAccuracyLongTail
    worst relative error over 20000 windows: P90=0.1979% P99=0.1990% (bound 0.5000%)
--- PASS: TestQuantileAccuracyLongTail (3.73s)
=== RUN   TestSpikeEvictionRecovery
    P99 baseline=19.83ms -> during spikes=8630.63ms
    P99 after eviction: estimated=19.7514ms exact=19.7863ms relErr=0.1761%
--- PASS: TestSpikeEvictionRecovery (0.00s)
=== RUN   TestEmptyAndEdgeCases
--- PASS: TestEmptyAndEdgeCases (0.00s)
PASS
ok  	slidinghist	3.893s
```

Acceptance criteria verified:

- Window capacity 1000 samples; 20k-sample long-tail (lognormal + spikes)
  stream: worst P90/P99 error vs exact (all samples retained, sorted) is
  **0.199% ≤ 0.5%**, matching the theoretical bound.
- Extreme spikes (~8s) push P99 from ~20ms to ~8.6s; after they slide out
  of the window P99 falls back to 19.75ms (0.18% from exact).
