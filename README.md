# convlib — exact signed integer convolution via multi-modulus NTT

Convolves `[]int64` sequences (negatives allowed) into exact `*big.Int`
coefficients. No silent truncation: results are either exact or an error.

## Coefficient bound and sign recovery

For every output coefficient,

    |c_k| = |sum_i a_i b_{k-i}| <= min(len(a), len(b)) * max|a_i| * max|b_j| = B

`Convolve` selects the shortest prefix of NTT primes whose product M
satisfies **M > 2B**, so every coefficient lies in (-M/2, M/2] and is
recovered exactly — sign included — by the balanced (symmetric) CRT
representative. If even the full prime table gives M <= 2B, it returns
`ErrModulusProductTooSmall` instead of wrapped values.

`Convolve` returns `(coeffs, B, M, err)` so callers can audit the bound
that was used.

## Verification

`go test ./...` cross-checks against `ConvolveNaive` (naive big-integer
double loop) on:

- 300 random short sequences with negative entries (`TestRandomShortSequences`)
- coefficients saturating B at >99.7% of M/2, all sign combinations
  (`TestNearModulusBoundary`)
- a bound beyond the full table product, which must error
  (`TestRefusesTruncation`)

`go run ./cmd/convcheck` prints the bound B and modulus product M used per
sequence.
