# wmpart

Partition an ordered, integer-weighted sample into at most `k` contiguous
non-empty buckets. Each bucket is represented by its **lower weighted
median** (for an even total weight, the smaller of the two middle
representatives), and the partition minimizes the total weighted absolute
error `sum w[i] * |x[i] - median(bucket(i))|`.

Tie-breaking among optimal partitions: fewer buckets first, then the
lexicographically smallest sequence of cut positions.

## Usage

```go
res, err := wmpart.Partition([]int64{1, 2, 3, 10}, []int64{1, 1, 1, 1}, 2)
// res.Buckets:   [{0,3} median 2 cost 2, {3,4} median 10 cost 0]
// res.TotalCost: 2
```

- `Partition` runs in `O(n^2*log n + n^2*k)` time via dynamic programming.
  Costs accumulate in `int64`; total weight must stay below `1<<62`.
- `BruteForce` enumerates every set of cut positions in `O(2^n * n)` time
  and accumulates costs with `big.Int`. It is the reference implementation
  used by the tests to cross-validate `Partition` on duplicate values,
  zero weights, and large integer costs.

## Testing

```sh
go test ./...
```
