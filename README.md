# minicoro

A tiny language with coroutines, implemented from scratch in pure
Python 3.11 (standard library only). No `eval`, no `compile`, no
`ast` module: the lexer, parser, and tree-walking interpreter are all
hand-written. Tests use `unittest`.

## Language

```
fn helper(x) {
    let y = yield x;        // suspend; resume(v) sends v back as the value
    return y + 1;
}

fn main() {
    let total = 0;
    let i = 0;
    while i < 3 {
        total = total + helper(i * 10);
        i = i + 1;
    }
    try {
        throw "boom";
    } catch e {
        print("caught", e);
    } finally {
        print("cleanup");
    }
    return total;
}
```

- Values: integers, strings, booleans (`true`/`false`), `null`.
- Statements: `let`, assignment, `if`/`else if`/`else`, `while`,
  `return`, `throw`, `try`/`catch`/`finally`, expression statements.
- Expressions: `+ - * / %`, comparisons, `and`/`or`/`not`, unary `-`,
  calls, and `yield <expr>` (an expression: it evaluates to the value
  passed to the next `resume`).
- `//` line comments; identifiers may be non-ASCII (e.g. Chinese).

## Coroutine semantics

- `interp.spawn("main", args)` creates a suspended coroutine with its
  own frame chain; interleaved coroutines never share locals.
- `co.resume(v)` runs to the next `yield` (status `"suspended"`) or to
  `return`/end (status `"returned"`, `terminal=True`). Suspension
  preserves the full interpreter stack: call frames, loop state, locals.
- Resuming/throwing/closing a **running** coroutine raises
  `ReentrancyError`.
- A language exception unwinds the stack, running every `finally` on
  the way out; the coroutine ends in state `"failed"`.
- `co.close()` on a suspended coroutine unwinds it, running each
  `finally` **exactly once**; the coroutine ends in state `"closed"`.
  Closing again is a no-op.
- Resuming a finished coroutine never restarts it: it returns a
  `ResumeResult` with `terminal=True` and the final status
  (`"returned"` keeps the return value, `"closed"`/`"failed"` give
  `None`).
- `co.throw(v)` injects a language exception at the suspension point;
  `catch e` binds `e` to `v`.

## Source positions

Every token and AST node carries `span = (start, end)` as **UTF-8 byte
offsets** into the source. `LangError` reports them, e.g.
`undefined variable '缺省' at bytes 40..46`.

## Layout

- `minicoro/lexer.py` — tokenizer (tracks UTF-8 byte offsets)
- `minicoro/parser.py` — recursive-descent parser
- `minicoro/nodes.py` — AST nodes
- `minicoro/interpreter.py` — generator-based evaluator + `Coroutine`
- `minicoro/errors.py` — `LangError`, `ReentrancyError`
- `tests/test_minicoro.py` — unittest suite
- `examples/demo.py` — runnable demo

## Run

```
python3.11 -m unittest discover -s tests -v
python3.11 examples/demo.py
```
