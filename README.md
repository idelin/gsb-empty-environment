# bucketpart

Partition an ordered sequence of integer-weighted samples into **at most k**
consecutive non-empty buckets, minimizing the total weighted absolute error
against each bucket's representative.

## Semantics

- **Representative**: each bucket is represented by its *lower* weighted
  median — the smallest value whose cumulative weight (in order, inclusive)
  reaches `(totalWeight+1)/2`. For even total weight this picks the smaller
  of the two middle values. A bucket with total weight zero costs 0.
- **Cost**: `sum(weight_i * |value_i - representative|)`, computed with
  `math/big`, so arbitrarily large weights/values cannot overflow.
- **Tie-breaking**: among minimum-cost partitions, prefer fewer buckets,
  then the lexicographically smallest sequence of bucket start indices.

## API

```go
type Sample struct {
    Value  int64 // non-decreasing across the input
    Weight int64 // non-negative
}

func Partition(samples []Sample, k int) (boundaries []int, cost *big.Int, err error)
```

`boundaries` holds the start index of each bucket (first is always 0);
`len(boundaries)` is the number of buckets actually used. Invalid input
(unsorted values, negative weights, `k < 1` with non-empty input) returns a
sentinel error. `k > len(samples)` is clamped.

## Verification

`bucketpart_test.go` cross-checks the dynamic program against an exhaustive
enumeration of all cut-point compositions (with an independent naive median
and cost implementation), covering repeated values, zero weights, and costs
exceeding 64 bits, plus a seeded fuzz run.

```sh
go test ./...
```
