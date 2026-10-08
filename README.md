# seglss

Optimal piecewise-constant segmentation of an integer sequence (Go library).

Minimizes, exactly:

    loss = SSE + penalty * (#segments),   each segment has >= m items

where SSE fits every segment by its mean. All arithmetic uses `math/big`,
so the loss and the segment means are exact rationals.

Tie-breaking: minimum loss, then fewer segments, then the
lexicographically smallest boundary (cut) sequence.

## Usage

```go
s, _ := seglss.NewSolver([]int64{1, 2, 10, 11, 3}, 1) // min segment length m=1
res, _ := s.Solve(big.NewRat(3, 2))                   // rational penalty per segment
fmt.Println(res)
// loss=11/2 sse=1 segments=3 {[0,2) mean=3/2 [2,4) mean=21/2 [4,5) mean=3}
```

`Solver` precomputes prefix sums once, so a set of penalty parameters can be
evaluated by repeated `Solve` calls while reusing the same interval costs
(`Solver.Cost(i, j)` is penalty-independent).

## Verification

`go test ./...` cross-checks the dynamic program against brute-force
enumeration of all cut points: exhaustively for all ternary sequences up to
length 8 (every feasible `m`, seven penalties), plus randomized sequences.
