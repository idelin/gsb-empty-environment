# decomp — exact mutual-independence decomposition of rational joint tables

Given a full joint probability table over at most 6 discrete variables
(each domain size ≤ 4) with exact rational entries, `decomp` finds the
**finest partition of the variables into mutually independent blocks**:

```
P(x) = ∏_b P(x_{B_b})        for every cell x (exact rational equality)
```

with the maximum possible number of blocks.

## Key design points

- **Exact arithmetic.** Everything is `math/big.Rat`. The independence
  test is a pure multiply-and-compare of the product of block marginals
  against the table on **every** cell. No conditional probabilities are
  ever formed, so **zero-probability cells cannot be divided away** or
  cause 0/0 — they are checked like any other cell.
- **Mutual, not marginal, independence.** A partition is accepted only
  if the full joint equals the product of *all* block marginals.
  Pairwise-independent-but-dependent cases (e.g. the XOR distribution
  uniform over {000,011,101,110}) correctly stay in one block
  (see `TestXORMarginalIndependenceIsNotMutual`).
- **Finest-ness by exhaustive enumeration.** All Bell(n) set partitions
  (n ≤ 6 ⇒ at most 203) are enumerated in canonical block-numbering
  order (lexicographic restricted growth strings) and tested. The result
  is the valid partition with the most blocks; ties are broken by the
  canonical RGS order. (Valid partitions are closed under common
  refinement, so the finest one is in fact unique; the tie-break keeps
  the output deterministic regardless.)
- **Exact equality certificates.** Each run emits a `Certificate`
  containing the block marginals, the full product identity, one exact
  split equation per decomposition step
  (`P(x_{B_i},rest) = P(x_{B_i}) · P(x_rest)`), and the complete
  enumeration evidence (all factoring partitions, max block count).
  `VerifyCertificate` re-checks every claim from scratch, independently
  of the search code path.

## Library use

```go
tab, _ := decomp.NewTable([]int{2, 2, 2}, probs) // probs: []*big.Rat, row-major, last var fastest
res, _ := tab.Decompose()
fmt.Print(res.Certificate.String())
ok := decomp.VerifyCertificate(tab, res.Certificate)
```

## CLI

```
echo '{"dims":[2,2,2],"probs":["1/4","1/4","0","0","0","0","1/4","1/4"]}' | go run ./cmd/decomp -v
```

```
Finest partition: {X0,X1} | {X2}
Certificate of decomposition (exact rational equalities):
  [full] P(x0,x1,x2) = P(x0,x1) * P(x2)   -- checked 8/8 cells, exact
  [split 1] P(x0,x1,x2) = P(x0,x1) * P(x2)   -- checked 8/8 cells, exact
Finest-ness proof: all 5 set partitions enumerated; 2 factor exactly;
  no valid partition has more than 2 blocks.
```

Without `-v` the CLI emits machine-readable JSON (partition, per-block
rational marginals, split equations, enumeration evidence).

## Tests

```
go test ./...
```

Covers Bell-number enumeration counts, full independence, the XOR
mutual-vs-marginal trap, zero-cell/constant-variable tables, correlated
pairs with structural zeros, non-uniform rational factors, six-variable
full splits, certificate tamper detection, and input validation.
