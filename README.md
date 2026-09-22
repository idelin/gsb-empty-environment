# PML: Pattern Matching Mini Language

PML is a small, self-contained expression language implemented from scratch in Python. The project includes its own lexer, recursive-descent parser, type checker, decision-tree pattern compiler, and tree-walking interpreter. It does not use `eval`, `exec`, Python's `compile`, or any external dependency.

## Features

- Tagged unions with zero-argument or payload-carrying constructors.
- Tuple values and nested tuple patterns.
- Integer, string, and Boolean literal patterns.
- Wildcard patterns written as `.`.
- Variable patterns with nested duplicate-binding rejection.
- Guards written with `| expression`.
- Compile-time unreachable clause detection.
- Compile-time exhaustiveness checking with concrete uncovered examples.
- Guards affect only runtime success; they never prove unconditional coverage.
- Source spans are represented as UTF-8 byte offsets.

## Syntax

```pml
union Shape {
  Circle(int),
  Rectangle(int, int),
  Empty
}

let shape: Shape = Rectangle(3, 4)

match shape: Shape {
  Circle(r) | r < 0 -> "negative",
  Rectangle(w, h) | w == h -> "square",
  Rectangle(w, h) -> "rectangle",
  Empty -> "empty"
}
```

Other supported expressions include tuples, parenthesized constructors, `if ... then ... else ...`, unary `-` and `!`, arithmetic, comparisons, equality, `&&`, and `||`.

## Run

```bash
python3 -m pml examples/shapes.pml
python3 -m pml examples/pairs.pml
python3 -m unittest discover -s tests -v
```

## Semantic Rules

- A wildcard before more specific later clauses makes those later clauses unreachable.
- A guarded wildcard is still reachable as a runtime fallback but is not unconditional coverage.
- A constructor clause with a guard does not cover that constructor unless another unguarded clause or wildcard covers the failing-guard case.
- Integer and string matches cannot be proven exhaustive from a finite list of literals; they need `.`.
- Boolean matches are exhaustive with both `true` and `false`.
- Variables in one clause use lexical match scope; duplicate nested names such as `P(x, x)` are rejected.
