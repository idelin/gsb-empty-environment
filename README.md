# bounded-cfg: exact bounded-derivation sampling for weighted CFGs

Python 3.11, standard library only (`fractions.Fraction`, `math`, `dataclasses`).

## The distribution

The target distribution lives on **complete derivation trees**, not on strings:

- tree weight = product of the weights of the productions it uses (exact rationals);
- the domain is all trees with terminal yield length `<= L` and at most `D`
  production applications — **epsilon productions count as one application**, so
  epsilon loops (`A -> A`, `A -> eps`) live in a finite, exactly computable
  domain and plain "sample until it terminates" is never needed;
- conditional probability of a tree = its weight / exact partition function `Z`.

Ambiguous strings are **not** deduplicated: a string with k derivations has
probability equal to the *sum* of the k tree weights (see
`TestAmbiguity`). Zero-weight productions contribute nothing; unreachable
nonterminals are inert.

No rational weight is ever converted to a float. Weighted choices scale all
candidate `Fraction`s by the lcm of their denominators and draw one exact
integer from the caller-supplied source.

## API sketch

```python
from fractions import Fraction
from bounded_cfg import Grammar, Production, BoundedModel

g = Grammar([
    Production("S", ("A",), Fraction(1)),
    Production("A", ("A", "A"), Fraction(1, 3), group="branch"),
    Production("A", (), Fraction(1, 5), group="eps"),
    Production("A", ("a",), Fraction(1)),
], start="S")

model = BoundedModel(g, max_length=2, max_steps=4,
                     tracked_groups=("branch", "eps"))

model.partition()                              # exact Z
model.partition_by_length_steps()              # exact Z[len, steps] grid
model.partition(required_groups=("branch",))   # conditional Z

r = model.sample(rng, length=1)                # rng: callable(n)->int in [0,n)
r.tree, r.terminals, r.steps, r.weight, r.probability   # all exact
model.sample(rng, required_groups=("branch",)) # condition is in the DP state
model.sample(rng, length=2, steps=1)           # -> None: explicit empty domain
```

`rng` is any callable `rng(n) -> int in [0, n)` or an object with
`randbelow(n)`/`randrange(n)` (e.g. `random.Random(seed)`). Out-of-range
values are rejected. The condition "group used at least once" is a bitmask
in the DP state — never rejection sampling.

## Independent verification

`src/independent_check.py` shares no code with the sampler: it brute-force
enumerates every bounded tree and aggregates weights. The test suite checks
the DP partition grid, conditional partitions, and per-tree probabilities
against it. Sampling-frequency tests exist but are marked supplementary.

## Resource costs

- **States**: at most `|N| (L+1) (D+1) 2^G` DP cells (`G` = number of tracked
  groups). Each tracked group doubles the mask lattice — keep `G` small.
- **Build time**: per production and depth, prefix convolutions over length,
  steps and masks — roughly `O(k L^2 D^2 4^G)` pair operations for a RHS of
  length `k` (arbitrary RHS lengths are handled by sequential convolution;
  subproblem tables are shared across all productions and depths).
- **Integer size**: cell weights are products of up to `D` input weights, so
  numerator/denominator bit length grows like `O(D * b)` where `b` is the
  largest input weight bit length. Sampling additionally scales by an lcm of
  denominators. This is the dominant hidden cost of exactness.
- **Budgets**: `max_work` caps convolution pair operations at build time and
  `max_sample_work` caps candidate enumeration per draw. Exceeding either
  raises `BudgetExceeded` — the library never returns truncated tables and
  pretends they are the requested distribution.

## Layout

- `src/bounded_cfg.py` — grammar, DP tables, exact sampler
- `src/independent_check.py` — brute-force enumerator/verifier (tiny grammars)
- `tests/test_bounded_cfg.py` — unittest suite (exact + supplementary statistical)
- `demo.py` — deterministic seeded example

## Run

```
python3.11 demo.py
python3.11 -m unittest discover -s tests -v
```
