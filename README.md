# expr_sandbox

A sandboxed expression executor for Python 3.11+ (standard library only).
Lexer, parser and evaluator are implemented from scratch — no `eval`, no
`compile`, no host reflection. Guest code can only call whitelisted pure
functions over plain data.

## Language

```
expression  := or_expr
or_expr     := and_expr ("or" and_expr)*
and_expr    := not_expr ("and" not_expr)*
not_expr    := "not" not_expr | comparison
comparison  := additive (("=="|"!="|"<"|"<="|">"|">=") additive)*
additive    := multiplicative (("+"|"-") multiplicative)*
multiplicative := unary (("*"|"/"|"//"|"%") unary)*
unary       := "-" unary | postfix
primary     := NUMBER | STRING | "true" | "false" | "null"
             | IDENT | IDENT "(" args ")" | "(" expression ")"
             | "[" [expression ("," expression)*] "]"
             | "if" "(" expression "," expression "," expression ")"
```

* Semantics follow Python where applicable (`and`/`or` return operand
  values, `/` is true division, `//`/`%` are floor division and modulo).
* `if(cond, a, b)`, `and`, `or` are lazy: dead branches never evaluate
  and never consume budget.
* String/list repetition must go through the budgeted `repeat()` builtin
  (`"ab" * 3` is rejected).
* Builtin whitelist: `len abs min max sum str int float bool upper lower
  strip startswith endswith contains split join concat repeat range
  reverse sort get slice`.

## Security model

* **Whitelist only**: unknown function names are rejected before
  evaluation (leftmost first, by UTF-8 byte offset). There is no
  attribute access, no indexing syntax on arbitrary objects, no imports.
* **One shared budget** (`Budget`) spans lexing, parsing, validation,
  lazy evaluation and every builtin. `Budget.consume` is atomic: limits
  are checked *before* allocation, so exponential growth (e.g.
  `repeat("ab", 10**9)` or doubling `concat`) fails before memory is
  committed. `BudgetExhausted` is always an exception — it is never
  treated as a falsy value by `and`/`or`/`if`/`not`.
* **Hard limits** (`Limits`): steps, source size, string length,
  collection size, nesting depth, number magnitude/digits.
* **No host stack crashes**: parser, evaluator, value validation and
  error walks are all iterative (explicit stacks); deep nesting raises
  `ExprError` instead of `RecursionError`.
* **No host leakage**: all errors are `ExprError` with messages built
  from source text only — never host paths, exception details or
  tracebacks. Source positions are UTF-8 byte intervals `[start, end)`.
* **Capability tokens**: host functions are registered through
  `FunctionRegistry` using a `Capability` issued by that registry;
  foreign or missing capabilities are rejected, and builtins cannot be
  overridden.
* **Values only across the boundary**: the executor passes only plain
  data (`None/bool/int/float/str/list`) to host functions; return values
  are validated and normalized (iteratively, cycle-safe), so host
  objects can never leak into the expression world. Exceptions raised by
  host functions are sanitized.
* **Reentrancy-safe**: `Executor.execute` keeps all state local; a
  registered function may call back into the same executor, and passing
  the caller's `Budget` keeps the total budget intact.

## Usage

```python
from expr_sandbox import Executor, FunctionRegistry, Budget, Limits

registry = FunctionRegistry()
cap = registry.issue_capability("my-app")
registry.register(cap, "double", lambda x: x * 2)

executor = Executor(registry=registry, limits=Limits(max_steps=50_000))
result = executor.execute("double(21) + len([1, 2])", budget=Budget(10_000))
print(result.value, result.consumed)   # 44 <steps>
```

## Demo and tests

```
python3 examples/demo.py
python3 -m unittest discover -s tests -t . -v
```

The differential test suite (`tests/test_differential.py`) compares the
iterative evaluator against an independent recursive interpreter on
hundreds of generated finite expressions: values, error messages, error
spans/order and exact step consumption must all agree.
