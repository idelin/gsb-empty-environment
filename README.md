# callbyneed

A call-by-need expression-graph interpreter in pure Python 3.11 (standard
library only). No `eval`, no `compile`, no parser generators: the lexer,
recursive-descent parser, and tree-walking interpreter are all implemented
by hand.

## Features

- **Call-by-need**: each `let` binding is a thunk shared by every reference
  (an expression *graph*, not a tree). A thunk is forced at most once; its
  value is memoized.
- **Cached exceptions**: if forcing a thunk raises, the exception object is
  memoized and the *same instance* is re-raised on every later force, without
  re-evaluating the expression.
- **Black-hole detection**: forcing a thunk that is already being forced
  (direct or indirect self-reference) raises `BlackHoleError` with the full
  forcing chain, e.g. `black hole: cyclic dependency x -> y -> x`.
- **Debug trace**: `--trace` prints the nested force/result/cache chain.
- **UTF-8 byte spans**: every token, AST node, and error carries a half-open
  `(start, end)` interval of UTF-8 *byte* offsets into the source.
- **Laziness**: unforced bindings never run — an unused `1 / 0` is harmless.

## Language

```
program  := expr
expr     := "let" binding ("," binding)* "in" expr | arith
binding  := IDENT "=" expr
arith    := term (("+" | "-") term)*
term     := factor (("*" | "/") factor)*
factor   := "-" factor | atom
atom     := INT | IDENT | "(" expr ")"
```

`let` is recursive: all bindings in one `let` are in scope for each other
and for the body. `/` is floor division on integers. `#` starts a comment
that runs to end of line.

## Layout

```
callbyneed/
  lexer.py         # source text -> tokens (UTF-8 byte spans)
  parser.py        # tokens -> AST (recursive descent)
  ast_nodes.py     # typed AST nodes with spans
  interpreter.py   # thunks, environments, forcing, memoization
  tracer.py        # forcing-chain trace + cycle reconstruction
  errors.py        # error types, all carrying byte spans
  __main__.py      # CLI: python -m callbyneed [--trace] [file]
tests/             # unittest suite (32 tests)
examples/          # demo.lzy, cycle.lzy, divzero.lzy
```

## How it works

A thunk is a state machine:

```
unevaluated --force--> blackhole --ok--> evaluated   (value memoized)
                          |
                          +--error--> failed         (exception memoized)
```

- Forcing an `evaluated` thunk returns the memoized value.
- Forcing a `failed` thunk re-raises the memoized exception instance.
- Forcing a `blackhole` thunk means the graph re-entered itself: the
  tracer's stack of in-progress thunks is scanned to rebuild the cycle
  (`x -> y -> x`) for the diagnostic.

Because bindings are only forced on demand, dead code never executes.

## Sample runs

```
$ python3.11 -m callbyneed --trace examples/demo.lzy
-> force c  (bytes 127..128)
  -> force b  (bytes 69..70)
    -> force a  (bytes 54..55)
    <- a = 42
  <- b = 84
  ~~ a  (cached value 42)
<- c = 126
~~ c  (cached value 126)
252
```

`examples/demo.lzy` contains `unused = 1 / 0`, which is never forced and
never raises; `c + c` forces `c` once and hits the cache the second time.

```
$ python3.11 -m callbyneed --trace examples/cycle.lzy
-> force x  (bytes 52..53)
  -> force y  (bytes 63..64)
  !! y raised BlackHoleError: black hole: cyclic dependency x -> y -> x at bytes 52..53
!! x raised BlackHoleError: black hole: cyclic dependency x -> y -> x at bytes 52..53
error: black hole: cyclic dependency x -> y -> x at bytes 52..53
```

```
$ python3.11 -m callbyneed --trace examples/divzero.lzy
-> force bad  (bytes 69..72)
!! bad raised DivByZeroError: division by zero in 1 / 0 at bytes 75..80
error: division by zero in 1 / 0 at bytes 75..80
```

## Tests

```
$ python3.11 -m unittest discover -s tests
.................................
----------------------------------------------------------------------
Ran 32 tests in 0.002s

OK
```

The suite covers, among other things:

- a thunk referenced ten times is evaluated exactly once
  (`test_thunk_referenced_ten_times_evaluated_once`)
- an unused `1 / 0` binding never raises
  (`test_unused_division_by_zero_never_raises`)
- a failed thunk re-raises the *same* exception instance and is not
  re-evaluated (`test_exception_is_cached_and_reraised_as_same_instance`)
- direct (`x = x`), indirect (`x -> y -> x`), and longer (`a -> b -> c -> a`)
  cycles all produce a `BlackHoleError` naming the full chain
- token and error spans are UTF-8 byte offsets, verified against sources
  containing multi-byte characters
