# Null-Rejecting Predicate Rewrites (bag semantics, no SQL engine)

Pure Python 3.11+ (stdlib only) study of when `sigma_p(R join S)` may be
rewritten. Relations are synthetic local bags of dict rows; `NULL` is an
explicit singleton (`ra/tvl.py`); predicates evaluate under SQL three-valued
logic (TRUE / FALSE / UNKNOWN); selection keeps only TRUE.

## Null-rejection analysis (`ra/expr.py`)

`is_null_rejecting(p, A)` decides whether `p` can never be TRUE when every
attribute in `A` is NULL. It is built from two mutually recursive,
syntax-directed predicates (`never_true` / `never_false`) plus
`may_null` / `must_null` on value expressions:

| construct        | never TRUE when A nulled            | never FALSE when A nulled           |
|------------------|-------------------------------------|-------------------------------------|
| `x <op> y`       | either operand must be NULL         | either operand must be NULL         |
| `x IS NULL`      | operand cannot be NULL              | operand must be NULL                |
| `x IS NOT NULL`  | operand must be NULL                | operand cannot be NULL              |
| `p AND q`        | either side never TRUE              | both sides never FALSE              |
| `p OR q`         | both sides never TRUE               | either side never FALSE             |
| `NOT p`          | p never FALSE                       | p never TRUE                        |

Consequences: `x > 3` is null-rejecting (NULL > 3 = UNKNOWN); `x IS NULL`
is not; `NOT` does not simply flip the verdict (`NOT (x IS NULL)` *is*
null-rejecting).

## Rewrite rules (`ra/rewrite.py`)

| rule | rewrite | precondition | counterexample when violated |
|------|---------|--------------|------------------------------|
| R1 | `sigma_p(R ⋈ S)` → `R ⋈ sigma_p(S)` | `attrs(p) ⊆ attrs(S)` | n/a (always valid for inner join) |
| R2 | `sigma_p(R ⟕ S)` → `sigma_p(R) ⟕ S` | `attrs(p) ⊆ attrs(R)` (preserved side) | n/a |
| R3 | `sigma_p(R ⟕ S)` → `sigma_p(R ⋈ S)` | `p` null-rejecting on `attrs(S)` | `p = x IS NULL`: padded rows satisfy `p`, inner join drops them |
| X1 (REJECTED) | `sigma_p(R ⟕ S)` → `R ⟕ sigma_p(S)` | none in general | (a) `x IS NULL` fabricates padded rows; (b) even `x > 3` wrongly keeps unmatched left rows |

Key points:
* Right-table `IS NULL` must never be pushed into the right input of a
  LEFT JOIN (X1a): filtering `S` first changes which left rows find a
  match, so previously matched left rows reappear as NULL-padded output.
* Right-table `x > 3` may not be pushed either (X1b), but because it *is*
  null-rejecting, R3 collapses the LEFT JOIN to an INNER JOIN and then R1
  pushes the predicate: `sigma_{x>3}(R ⟕ S) ≡ R ⋈ sigma_{x>3}(S)`.
* Bag semantics: duplicate rows are significant; join multiplicities
  multiply; each unmatched left occurrence is padded exactly once.

## Files

* `ra/tvl.py` — `NULL` sentinel, `Tri` three-valued logic
* `ra/expr.py` — predicate AST, evaluation, null-rejection analysis
* `ra/rel.py` — bag `select` / `inner_join` / `left_join`, `bag_eq`
* `ra/rewrite.py` — rules R1–R3 and rejected X1, each with checkable precondition
* `samples.py` — runnable demo on synthetic relations
* `test_rewrites.py` — 27 unittests: equivalences, multiplicities, counterexamples

## Run

```
python3 samples.py            # demo: relations, preconditions, outcomes
python3 -m unittest -v test_rewrites
```
