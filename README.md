# interval-analysis

Interval analysis for small integer programs (Python 3.11, standard library
only; tests use `unittest`).

## Features

- **Interval abstract domain** (`interval_analysis/interval.py`): integer
  intervals with unbounded endpoints, C-style truncating division/modulo,
  join/meet, widening and narrowing.
- **Abstract interpreter** (`interval_analysis/analyzer.py`):
  - branch contraction: variable intervals are refined under branch
    conditions (`==`, `!=`, `<`, `<=`, `>`, `>=`, and truthiness as `!= 0`);
  - loops are solved by fixed-point iteration with widening at the loop
    head (guarantees termination of the ascending phase) followed by a
    configurable number of optional narrowing iterations
    (`Analyzer(narrowing_iterations=0)` disables it);
  - diagnostics for division/modulo by zero and failing assertions.
    Because the domain over-approximates, alarms are marked `possible`
    unless the bad case is unavoidable (`definite`);
  - refinement that the domain cannot represent exactly -- e.g. `x != 0`
    on an interval straddling zero, whose exact result is the non-convex
    union `[-3,-1] U [1,3]` -- keeps the **sound** interval (0 is *not*
    silently dropped) and records a precision note. It is never presented
    as exact;
  - every loop reports its fixpoint status: iteration count, whether
    widening/narrowing were applied, and the resulting invariant.
- **Finite-state verifier** (`interval_analysis/verifier.py`): explicit-state
  exploration of the program's control-flow graph. All nondeterminism comes
  from `Nondet(lo, hi)` with finite ranges, so exploration always terminates:
  - `SAFE` -- state space exhausted, no violation;
  - `UNSAFE` -- assertion failure or division by zero reached; the
    counterexample is the exact sequence of nondet choices and **replays
    deterministically** via `Counterexample.replay(program)`;
  - `UNKNOWN` -- the `max_states` budget was exhausted before the search
    completed; the verdict stays pending and no safety claim is made.
- **Concrete interpreter** (`interval_analysis/lang.py`): executes programs
  and replays recorded choice sequences deterministically; division and
  modulo truncate toward zero, matching the abstract semantics.

## Layout

```
interval_analysis/
  interval.py    # interval abstract domain
  lang.py        # AST, concrete interpreter, replay
  analyzer.py    # abstract interpreter (widening/narrowing, diagnostics)
  verifier.py    # explicit-state finite-state verifier
examples/run_samples.py
tests/           # unittest suite
```

## Usage

```python
from interval_analysis.analyzer import Analyzer
from interval_analysis.lang import Assign, BinOp, Compare, Const, Var, While, seq

program = seq(
    Assign("x", Const(0)),
    While(Compare("<", Var("x"), Const(100)),
          Assign("x", BinOp("+", Var("x"), Const(1)))),
)
result = Analyzer(narrowing_iterations=3).analyze(program)
print(result.exit_state["x"])   # Interval([100, 100])
print(result.fixpoints[0])      # widening applied, narrowing recovered bounds
```

## Run the samples

```
python3 examples/run_samples.py
```

## Run the tests

```
python3 -m unittest discover -s tests -v
```
