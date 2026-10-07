# counter

Go library for reasoning about fixed-width unsigned counters (e.g. metrics
exporters) whose readings may wrap around **multiple** times between samples
and may be reset by restarts.

## Model

Each `Sample` carries a timestamp, a raw counter value, and a `Generation`
marker identifying the counter incarnation. Given two adjacent samples, a
bit width `w` (modulus `M = 2^w`) and a maximum growth rate, `Delta`
returns every increment consistent with the rate limit:

- Same generation: the modular difference `base = (curr - prev) mod M` is
  the zero-wrap increment; any `base + k*M` within the rate budget
  `maxRate * dt` is also admissible, since the counter may have wrapped
  `k` extra times. Results are returned as an `Interval{Min, Max,
  MinWraps, MaxWraps, Modulus}`; `Interval.Values()` enumerates them.
- Generation changed: the counter restarted; the increment is unknowable
  and `ErrRestart` is returned.
- `base` alone exceeds the budget: no number of wraparounds can explain
  the jump (wraps only make increments larger), so `ErrUnexplainable` is
  returned.

## Example

```go
prev := counter.Sample{Time: 0, Value: 15, Generation: 1}
curr := counter.Sample{Time: 4, Value: 2, Generation: 1}
iv, err := counter.Delta(prev, curr, 4, 10) // 4-bit counter, <=10/s
// iv.Values() == [3 19 35]: one, two or three wraparounds all fit the budget.
```

## Testing

`counter_test.go` brute-force enumerates every increment in `[0, budget]`
for all reading pairs at widths 1–6 and compares against `Delta`, covering
same-generation wraps, restarts, and unexplainable jumps:

```sh
go test ./...
```
