# gdict — global dictionary consolidation

`gdict` is a Go package an optimizer can import to merge several partition-local
dictionaries into one physical global code space while preserving every live
old lookup and range scan.

```go
import "prescreen.local/ps-bf896dd1e3/gdict"

plan, err := gdict.Solve(cfg)
if err != nil { /* *gdict.IncompatibleError or a search/input error */ }
if err := gdict.Verify(cfg, plan); err != nil { /* independent audit */ }
```

The package has no dependencies outside the Go standard library. A minimal
configuration looks like this:

```go
a, b := gdict.Text("a"), gdict.Text("b")
pre, _ := gdict.NewPreorder("en", [][]gdict.Key{{a}, {b}})
cfg := gdict.Config{
    Partitions: []gdict.Partition{{
        Name: "p1", Version: "en",
        Entries: []gdict.Entry{{OldCode: 0, Key: a}, {OldCode: 1, Key: b}},
        Cuts:    []gdict.Cut{{Name: "live", OldCode: 0, Bound: gdict.LE}},
    }},
    Preorders: []gdict.Preorder{pre},
    Space:     gdict.CodeSpace{Limit: 100},
}
plan, err := gdict.Solve(cfg)
```

## Formal input

A `Key` is either a string (`Text`) or `NULL` (`Null`). The empty string is a
text key and is distinct from `NULL`.

* `Preorder{Version, Ranks}` is a total preorder. Smaller rank sorts first;
  equal ranks are equivalent under that collation version. Every key mentioned
  anywhere in the configuration must be ranked by every supplied preorder.
* `Partition{Name, Version, Entries, Cuts}` is one old local dictionary.
  Entries are ordered by `OldCode`. One logical key may occur more than once,
  but a listed cut may not split two occurrences of the same logical key:
  they share one physical code and therefore cannot land on opposite sides of
  a translated cut.
* `Alias{Members}` declares spellings of one logical dictionary value. Alias
  members must be equivalent in every collation; otherwise the input is
  incompatible. Alias members share a physical code.
* `Cut{OldCode, Bound}` is a still-live snapshot range cut. `LT` means
  `{row : old code < OldCode}`; `LE` means `{row : old code <= OldCode}`.
* `Lookup{Partition, Version, Key}` is a still-live exact lookup. Its old
  semantics are collation semantics: every row whose key is equivalent to
  `Key` under `Version` is returned (aliases included).
* `CodeSpace{Limit, Holes, Fixed}` is the global physical domain
  `[0, Limit)`, with inclusive reserved holes and immovable foreign-key codes.
  `Limit` may be as large as `2^50`.

Distinct keys that are equivalent in **every** collation may share a physical
code. This is the “equivalent keys need not have the same code” freedom: the
optimizer may merge them to reduce used codes, but a live cut or a fixed code
may force them to be split. Keys that are strictly ordered by any collation
must receive ordered, distinct codes.

## Translation pages

A page is a slope-one affine translation of one old source interval:

```go
type Page struct {
    OldLo, OldHi uint64
    Delta        int64  // new code = old code + Delta
    NewLo, NewHi uint64 // review convenience
}
```

Pages are sorted and non-overlapping. Every dictionary entry and every listed
cut is covered by exactly one page. A cut at old code `b` is translated to
`b + Delta` with the same `LT`/`LE` operator. No query result is stored: the
optimizer derives every translated bound from these intervals and increments.

## What is preserved

All constraints are checked against concrete old row identities (`partition`,
`old code`):

1. **Collation order.** If a collation strictly orders `a < b`, then
   `code(a) < code(b)` for every active key.
2. **Range cuts.** For every cut, the old prefix and the page-translated new
   prefix contain exactly the same entry rows. This is stronger than merely
   separating the two sides: the same `LT`/`LE` operator is retained.
3. **Lookups.** A lookup returns the same rows under collation equivalence.
4. **Fixed codes.** A fixed key keeps its code; no other non-equivalent key may
   occupy it. No code lies in a reserved hole.

Because every prefix cut is preserved, any range scan expressed as the
difference of two listed cuts is preserved as well.

## Objective

Results are compared lexicographically by:

1. minimum exception pages: `sum(max(0, pages(p)-1))`;
2. minimum distinct physical codes actually used;
3. lexicographically smallest mapping, compared over mapped entries ordered by
   partition name and old code.

The resulting `Plan` is deterministic for the same input.

## Algorithm

1. Normalize aliases, validate preorders and code-space constraints.
2. Build a DAG over logical key groups:
   * an edge `a -> b` for every strict collation observation `a < b`;
   * an edge `l -> r` whenever a listed cut puts old row `l` on the left and
     `r` on the right.
   A directed cycle is immediately returned as an incompatibility certificate.
3. Group keys by their rank vector across all versions. These universal
   equivalence classes are the only places where key order is free. At most
   twelve multi-key conflict classes are accepted.
4. Enumerate the linear extensions of each conflict class, then search code
   assignments with branch-and-bound. Candidate codes are generated from
   `old code + active affine delta`, fixed/hole landmarks, and compact allowed
   codes; the sparse `2^50` domain is never enumerated.
5. For each candidate assignment, derive the minimum page segmentation. This
   is a greedy interval-intersection problem: entry positions require one
   exact delta, cut positions allow an interval of deltas, and cutting at the
   first empty intersection is optimal. Cut-only pages are inserted where two
   adjacent runs cannot translate a cut.
6. Return the best `(exception pages, used codes, mapping)` tuple.

The search is exact while the `MaxStates` budget (default `2,000,000`) is not
exhausted. If exhausted, `Solve` returns `ErrSearchLimit`; it never returns a
suboptimal plan as if it were optimal.

## Incompatibility certificates

When no mapping exists, `Solve` returns `*IncompatibleError` containing either:

* at least two concrete observations — e.g. two collation orders, or two range
  cuts with partition, cut name, old codes, and the conflicting keys; or
* fixed-code constraints — e.g. two immovable codes contradicting an order,
  a fixed code inside a reserved hole, or a code collision.

Certificates are designed to be checked directly against the input, without
re-running the optimizer.

## Independent review

`Verify(cfg, plan)` re-normalizes the input, recovers every group code from the
plan, and independently checks:

* code-space, hole, and fixed-code constraints;
* all collation and cut ordering edges;
* every page-to-entry mapping;
* every translated `LT`/`LE` cut row set;
* all lookups;
* stats and the SHA-256 `Plan.Digest`.

`Plan.String()` prints a stable human-readable mapping with pages, entries, and
the digest.

## Assumptions and limits

* Every preorder ranks every mentioned key.
* Repeated occurrences of one logical key in a partition must not be split by
  a listed cut; such a cut is returned as a range-observation certificate.
* Old codes and cut codes fit in `int64`; global codes are below `Limit` and
  `Limit <= 2^50`.
* Pages have slope one (`new = old + Delta`). This is the affine increment the
  snapshot index translator can execute without per-query state.
* Alias members must be collation-equivalent in every version.
* The exact search enumerates at most twelve multi-key conflict classes and is
  bounded by `Config.MaxStates`.

## Verification tests

`go test ./...` runs, among others:

* `TestEnumerateSplitNullEmptyHole` — exhaustive small-code enumeration where
  two versions split one key group, with `NULL`, empty string, and a hole;
* `TestOracleSmall` — over a thousand randomized tiny instances compared
  against a full enumeration oracle (collations, shared keys, cuts, holes,
  fixed codes);
* `TestLTvsLEAtSameCode`, `TestNullPositionAndEmptyString`,
  `TestSparseUint64AndHoleBoundaries`, `TestAliasAndLookup`,
  `TestDeterministic`;
* conflict-certificate and tamper-detection tests.
