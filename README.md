# conv — exact big-integer polynomial products over a coefficient interval

`conv.Product(ctx, a, b, lo, hi, opts)` computes the coefficients of the
product `a*b` at degrees `[lo, hi)` exactly, without computing the rest
of the convolution. Coefficients are `*big.Int`; signs and sparsity are
handled natively.

## Algorithm

Multi-modulus convolution with Garner CRT reconstruction:

1. **Coefficient bound (proof of no wraparound).** For
   `c_k = sum_{i+j=k} a_i*b_j`, distinct summands use distinct indices,
   so `|c_k| <= max|a_i| * max|b_j| * min(nnz(a), nnz(b)) =: B`
   (`coeffBound` in `conv.go`).
2. **Modulus chain.** `newChain` picks 31-bit primes until the product
   `M` satisfies `M > 2*B`. If the prime pool cannot reach the bound it
   returns an error — the library never reconstructs with an
   insufficient modulus, so silent wraparound is impossible.
3. **Interval-restricted modular convolution.** Modulo each prime, only
   index pairs `(i, j)` with `i+j` in the current block of `[lo, hi)`
   are accumulated (`plan.convolveBlock` in `chain.go`).
4. **Exact reconstruction.** Garner's algorithm lifts the residues to
   the unique representative `r` in `(-M/2, M/2]`. Since
   `|c_k| <= B < M/2`, `c_k` lies in that interval and `r == c_k`.

## Cancellation and partial state

Work proceeds in blocks of `Options.BlockSize` coefficients; the
context is checked between blocks. On cancellation `Product` returns
the partial `*Result` plus the context error:

- `Result.Done` lists every completed sub-interval (the exact zero tail
  beyond `deg(a)+deg(b)` is completed immediately).
- `Result.At(k)` reports whether the coefficient of `x^k` is available;
  coefficients of cancelled blocks are `nil` in `Result.Coeffs`.

## Testing

`go test ./...` cross-checks against the naive `big.Int` convolution
(`Naive` in `naive.go`): randomized small cases, negative coefficients,
sparse high-degree terms, ~220-bit coefficients (forcing long modulus
chains), the bound/minimality invariant `M > 2*B`, and the completed
interval state after mid-run cancellation.
