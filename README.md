# Exact Alias Table (Python 3.11, stdlib only)

Constant-step sampling with replacement from rational weights, with zero
floating-point error: construction uses exact rational capacity splitting
(Vose's algorithm over `fractions.Fraction`), and sampling consumes exactly
two unbiased integers per pick from a caller-injected random source.

## Files

- `alias_table.py` — `AliasTable`, `SeededRandomSource`,
  `AliasTablePublisher` / `SamplingSession` (dual-version publishing).
- `test_alias_table.py` — unittest suite; tests are labeled `[EXACT]`
  (full enumeration of all random choices, or algebraic reconstruction)
  or `[EMPIRICAL]` (statistical agreement only).
- `demo.py` — runnable end-to-end sample.

## Guarantees

- Weights are exact rationals (`int`, `Fraction`, finite `float`,
  finite `Decimal`, or `"p/q"` strings); negative and non-finite
  weights are rejected, and at least one weight must be positive.
- The table's reconstructed probabilities equal the normalized weights
  exactly — tiny weights (e.g. `1/10**30`) and zero weights introduce
  no bias; a zero-weight item is never selected, and a single positive
  item is always selected.
- Tables serialize to JSON (`dumps`/`loads`) and restore bit-exactly;
  a seeded run is replayable after a round trip. Restored tables are
  verified to reproduce their weights, so corruption is detected.
- `AliasTablePublisher.update_weights` builds the new table fully and
  swaps it in atomically; sessions opened earlier keep sampling the old
  distribution. Failed updates leave the old version published.
- The random source is injected by the caller (`randbelow(n)` protocol);
  `SeededRandomSource` records the seed and draw count, and each
  `SamplingSession` records its sample count.

## Usage

```python
from fractions import Fraction
from alias_table import AliasTable, AliasTablePublisher, SeededRandomSource

table = AliasTable([Fraction(1, 10**12), 3, 0, Fraction(2, 7)])
source = SeededRandomSource(seed=42)
items = table.sample(source, 1000)          # constant-step draws
assert table.probabilities() == table.normalized_weights  # exact

restored = AliasTable.loads(table.dumps())  # bit-exact round trip
assert restored.sample(SeededRandomSource(42), 1000) == items

publisher = AliasTablePublisher([1, 1, 1])
session = publisher.open_session()          # pins version 1
publisher.update_weights([10, 1, 1])        # publishes version 2
session.sample(SeededRandomSource(1), 5)    # still the old distribution
```

## Run

```sh
python3.11 demo.py                  # end-to-end sample
python3.11 -m unittest -v           # full test suite
```
