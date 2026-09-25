# constkit — multi-module constants with conditional compilation

A small, self-contained language and incremental compiler implemented in pure
Python 3.11 standard library. The lexer, parser and tree-walking interpreter
are hand-written; `eval`/`exec`/`compile` are never used. All source positions
are **half-open UTF-8 byte ranges**.

## Language

```
module M;
const A = B + 1;              # forward reference; const = exported
local cache = 10;             # local: usable inside the module, not exported
enum Color { Red, Green = 5, Blue }
const arr = [1, 2, 3];
const x = arr[0] + Color.Blue;
when !fast { const B = 2; }   # conditionally compiled block
when fast  { const B = A; }  # (toggling 'fast' makes A <-> B a cycle)
```

- Integers are arbitrary precision; `/` and `%` use floor semantics.
- References: `Name` (current module, local or exported),
  `Module.Name` (exported consts only), `Enum.Member`,
  `Module.Enum.Member`.
- `when` conditions: flag names with `!`, `&&`, `||`, parentheses.

## Engine semantics

- **Fingerprint incrementality**: each constant's fingerprint hashes its
  expression AST, the fingerprints of its resolved dependencies, referenced
  enum values, gating flag values and the overflow policy. Matching
  fingerprints reuse the prior value without interpreting the expression
  (`Engine.eval_count`, `last_evaluated`, `last_reused` prove this).
- **Snapshots**: failed constants keep the previous good value; constants that
  disappear are moved to `Snapshot.retired`. Errors never overwrite usable
  artifacts.
- **Diagnostics are rebuilt every apply**, so flipping a flag removes old
  diagnostics (deleted dependencies) and adds new ones (new cycles) exactly.
- **Validate-then-publish batches**: `apply(..., require_clean=True)` compiles
  into a staging area and only publishes when there are zero diagnostics.
- **Cancellation**: pass a `CancelToken`; a cancelled staging run publishes
  nothing and rolls back the evaluation counter.
- **Checkpoints**: `save_checkpoint`/`load_checkpoint` persist JSON
  (sources, flags, snapshot, diagnostics, counter). A restored engine
  produces identical artifacts and identical incremental behavior.
- **Overflow policies**: `exact` (default), `wrap` (signed N-bit), `error`.

## Layout

- `constkit/diag.py` — `Span` (UTF-8 bytes) and `Diagnostic`
- `constkit/lexer.py` — hand-written lexer
- `constkit/parser.py` — AST + recursive-descent parser
- `constkit/eval.py` — interpreter, overflow policy, cancellation, zero-chain
- `constkit/engine.py` — dependency graph, fingerprints, snapshots, checkpoints
- `demo.py` — nine replayable end-to-end samples
- `tests/` — unittest suites (29 tests)

## Run

```
python3.11 demo.py
python3.11 -m unittest discover -s tests -v
```
