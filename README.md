# seqrank

Exact randomization inference for a sequentially monitored two-sample
rank-sum (Wilcoxon/Mann-Whitney) test, written in Go.

## Model

- `n` observations arrive in a fixed time order (`Config.Values`).
- All samples share **one final random assignment** into a treated group of
  size `Treat` and a control group of size `n - Treat`; every prefix of the
  time order is labeled by that same assignment.
- At each specified look time `k` (a prefix size), the treated rank-sum `W`
  is computed from the **average (mid)ranks of the pooled prefix** — ranks
  are recomputed inside every prefix, since an early sample's rank changes
  as later observations arrive.
- The monitored statistic is `|W - E[W]| / Norm` with
  `E[W] = m(k+1)/2` (`m` treated in the prefix) and a user-supplied positive
  rational `Norm`. Monitoring stops at the first look where
  `stat >= Threshold`; **equality counts as crossing** (two-sided).
- A look at a prefix where either group is empty is skipped.
- Tied values share average ranks but remain **distinct samples**: the
  assignment enumeration ranges over sample identities.

## Exactness

The library enumerates all `C(n, Treat)` final assignments independently
and replays every prefix for each of them, producing the exact joint
randomization distribution of:

- the first-crossing (stopping) time, and
- the ever-crossing event,

plus the never-crossing probability mass. Per-look p-values are **never**
multiplied together: repeated looks are dependent because all prefixes
share one assignment, and only joint enumeration respects that dependence.

All arithmetic is exact. `2*rank` is an integer, so the crossing condition
reduces to an integer comparison against `ceil(2 * Threshold * Norm)`.

## Budgeted enumeration

`State.Step(plan, budget)` processes at most `budget` assignments per call.
A `State` carries exact counts plus a resume cursor and is gob-encodable,
so unfinished enumeration (including partial counts) can be saved and
continued later. Probability accessors (`ProbEverCrossed`,
`ProbFirstCross`, `Distribution`, …) return `ok = false` until
`State.Complete` — a partial enumeration is never reported as an exact
probability.

## Usage

```go
cfg := &seqrank.Config{
    Values:    []float64{2.1, 1.5, 3.3, 1.5, 2.8, 0.9}, // time order
    Treat:     3,
    Looks:     []int{2, 4, 6},                          // strictly increasing
    Threshold: big.NewRat(1, 2),
    Norm:      big.NewRat(3, 2),
}
s, err := seqrank.Run(cfg)              // full enumeration
first, never, ok := s.Distribution()    // P(first cross at k), P(never)
p, ok := s.ProbFirstCross(4)            // P(observed stopping time = 4)
```

Budgeted / resumable:

```go
plan, _ := seqrank.Prepare(cfg)
st := seqrank.NewState(plan)
for !st.Complete {
    st.Step(plan, 1_000_000) // save st (e.g. gob) between steps
}
```

`n <= 24` keeps full enumeration comfortable (`C(24,12) ≈ 2.7M`
assignments, well under a second); larger `n` works through the budgeted
interface.
