# ftoa

`ftoa` converts any finite IEEE 754 binary64 value into the **shortest
decimal string that round-trips** to exactly the same value.

```go
s := ftoa.Format(f)
```

## Rules

- **Shortest**: fewest significant digits such that parsing the string
  yields the original value bit-for-bit.
- **Closest**: among all shortest candidates, the one numerically closest
  to the value wins.
- **Even last digit**: if two candidates are equally close, the one whose
  last digit is even wins.
- **Signed zeros preserved**: `0` formats as `"0"`, negative zero as `"-0"`.
- **Non-finite labels**: NaN → `"NaN"`, ±Inf → `"+Inf"` / `"-Inf"`.
- Layout matches Go's `'g'` verb for shortest output: scientific notation
  when the decimal exponent is `< -4` or `>= 6`, plain notation otherwise.

## How it works

The core search is implemented from scratch with exact big-integer
arithmetic (`math/big`); no standard-library float↔string conversion is
used in the implementation.

1. Decompose `f = m * 2^e2` exactly (via `math.Frexp`).
2. Compute the rounding interval `(lo, hi)`: every real number inside it
   rounds to `f` under round-to-nearest-even. Boundaries are midpoints to
   the neighbouring floats (`math.Nextafter`); they are inclusive iff the
   mantissa is even. Subnormals use the pinned spacing `2^-1074`; normal
   powers of two halve the lower gap.
3. For precision `p = 1..17`, scale the interval by `10^-q` and compute the
   integer range of `p`-digit mantissas `k` with `k * 10^q` inside the
   interval. The first `p` with a non-empty range is the shortest length.
4. Pick the `k` closest to `f / 10^q`; equidistant ties go to the even `k`.

## Verification (tests)

The standard library is used **only as a reference oracle** in tests:

- `TestAgainstStdlib` — curated values: powers of two/ten with neighbours,
  smallest/largest subnormals, smallest normal, `MaxFloat64`, etc.
- `TestRoundTripProperty` — 200k random bit patterns parse back bit-exactly.
- `TestRandomAgainstStdlib` — 200k random values vs `strconv`.
- `TestAdjacentFloats` — adjacent floats never share a representation.
- `TestSubnormals` — exhaustive sweep of the 20k smallest subnormals, the
  subnormal/normal boundary, and 100k random subnormals.

Rare disagreements with Go 1.11's pre-Ryu formatter (which occasionally
misses the closest candidate) are re-verified against the spec with
independent exact rational arithmetic (`specCheck`).
