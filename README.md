# OTel Metrics Temporality Converter

Zero-dependency, importable TypeScript library implementing stateful
OpenTelemetry metric temporality conversion between **Cumulative** and
**Delta** aggregation, with the restart / late-point / bucket-change /
high-cardinality protections that a collector-side stateful processor needs.

- `CumulativeToDeltaProcessor` — Cumulative input, Delta output (Prometheus-style
  cumulative streams into StatsD/Datadog-style delta pipelines).
- `DeltaToCumulativeProcessor` — Delta input, Cumulative output.
- Bounded LRU series store (`maxSeries`) with explicit eviction/reentry handling.
- Bigint nanosecond timestamps; no external runtime dependencies.

## Build and test

```bash
npm run build      # tsc -> dist/ (JS + .d.ts + source maps)
npm run typecheck  # strict type check, no emit
npm test           # builds, then runs the node:test suite under dist/test
```

The environment has no network access, so the package deliberately ships
without npm dependencies; it uses the system TypeScript compiler and the
built-in `node:test` runner.

## Data model

Points mirror the OpenTelemetry metric data point structure:

```ts
import type { Metric } from './dist/src/index.js';

const cumulativeMetric: Metric = {
  name: 'http_requests',
  instrumentType: 'COUNTER', // COUNTER | UP_DOWN_COUNTER | HISTOGRAM | GAUGE
  temporality: 'CUMULATIVE',
  dataPoints: [
    { timestamp: 100n, startTime: 0n, value: 10, attributes: { host: 'a' } },
  ],
};
```

Histogram points use explicit buckets:

```ts
{
  timestamp: 200n,
  startTime: 0n,
  explicitBounds: [10, 100],
  bucketCounts: [1, 2, 3], // length = bounds.length + 1 (overflow last)
  count: 6,                // must equal the bucket count sum
  sum: 320,
  min: 2,
  max: 95,
}
```

## Usage

```ts
import {
  CumulativeToDeltaProcessor,
  DeltaToCumulativeProcessor,
  createTemporalityProcessor,
} from 'otel-temporality';

const toDelta = new CumulativeToDeltaProcessor({
  maxSeries: 10_000,
  initialPointAsDelta: false,
  onDrop: (info) => console.warn('dropped late point', info),
});

const deltaMetric = toDelta.process(cumulativeMetric);

const toCumulative = createTemporalityProcessor('CUMULATIVE', { maxSeries: 10_000 });
const backToCumulative = toCumulative.process(deltaMetric);
```

Every emitted point carries conversion metadata: `restart: boolean` and, when
`restart` is true, `resetReason` (`first_point`, `restart_start_time`,
`restart_value_decrease`, `histogram_bounds_change`, `eviction_reentry`).
`processor.getStats()` returns lifecycle counters and `resetStats()` snapshots
and zeroes them; `clearState()` drops all series while keeping stats.

## Conversion semantics

### Cumulative -> Delta

- **First point** establishes a baseline and emits nothing by default. With
  `initialPointAsDelta: true` it is emitted as the initial segment only when
  `startTime < timestamp`; an equal/missing start time stays a baseline.
- **Monotonic instruments** (`COUNTER`, `HISTOGRAM`) detect a restart when the
  `startTime` changes *or* any value/bucket/count/sum decreases
  (`curr < prev`). A restart never emits a negative number: the new point is
  emitted as the full value since the new `startTime`. When only the value
  fell while `startTime` stayed the same, the previous point timestamp becomes
  the new segment start and the point is flagged `restart_value_decrease`.
- **`UP_DOWN_COUNTER`** is non-monotonic: points are plain algebraic
  differences, so decreases produce legitimate negative deltas, never restarts.
- **Late/out-of-order points** (`timestamp <= lastTimestamp` for the same
  series) are rejected without touching the baseline; they increment
  `droppedOutOfOrder` and fire `onDrop`. A late point can never look like a
  restart because the check runs before any state transition.
- **Histogram bucket changes** (any difference in `explicitBounds`, checked
  element by element) reset the series with the new point as a full segment,
  flagged `histogram_bounds_change`, instead of subtracting misaligned buckets.

### Delta -> Cumulative

- `COUNTER` deltas accumulate monotonically; `UP_DOWN_COUNTER` deltas are
  summed algebraically and may be negative.
- Histograms accumulate each bucket count plus `count`/`sum`; `min`/`max`
  combine via min/max over the lifecycle.
- `explicitBounds` must match element by element. A boundary change restarts
  the series with a fresh `startTime` (the arriving window start) instead of
  adding counts into misaligned buckets, flagged `histogram_bounds_change`.
- Late points are rejected with the same stats/callback as the reverse path.

### LRU eviction and reentry

When the tracked series count reaches `maxSeries`, the least-recently-updated
series is evicted (insertion-order `Map`, refreshed on every touch). The
processor remembers recently evicted series identities so a returning series
is handled deliberately:

- **Cumulative -> Delta:** the returning first point rebuilds a baseline and
  emits nothing, so a large cumulative reading cannot become a full-value
  spike; the next point produces a normal delta.
- **Delta -> Cumulative:** the returning point starts a new lifecycle at the
  arriving window's `startTime` and is flagged `eviction_reentry`, so a backend
  never interprets the implicit zero as a no-restart continuation.

## Layout

- `src/types.ts` — data model, stats and option types
- `src/lru.ts` — bounded LRU cache
- `src/series.ts` — deterministic attribute-based series keys
- `src/validate.ts` — point/histogram invariants and structural cloning
- `src/base-processor.ts` — shared LRU, eviction marker and stats scaffolding
- `src/cumulative-to-delta.ts`, `src/delta-to-cumulative.ts` — state machines
- `test/` — node:test acceptance suite (32 cases) covering both restart kinds,
  late-point immunity, negative UpDownCounter deltas, bucket-boundary resets,
  LRU reentry spike protection and round-trip conservation
