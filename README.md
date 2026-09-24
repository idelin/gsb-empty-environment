# minivm

A small language virtual machine in pure Python 3.11 (standard library
only, no `eval`, no third-party compilers) featuring:

- **Lexical closures over mutable bindings** — compile-time resolved
  `(depth, slot)` addressing; closures capture shared environment cells.
- **Recursion & deep recursion** — the interpreter is fully iterative
  (explicit frame stack), so guest recursion is not bounded by Python's.
- **Exceptions & cleanup** — `throw` / `try` / `catch` / `finally`;
  `finally` runs exactly once on normal exit, exceptions, and cancel.
- **Coroutines** — `yield` / `resume` / `close`.
- **Serializable suspension snapshots** — a suspended coroutine
  (closure environments as a sharing/cycle-preserving graph, call
  frames, pending `finally` handlers) serializes to plain JSON.
  Host functions are never serialized (snapshot raises instead).
  Restore validates the **bytecode version** and the **program digest**
  (SHA-256 of canonical bytecode), so a changed program is rejected.
- **Resource limits** — per-resume step budget and a machine-wide heap
  quota; faults unwind through `finally` and leave no runnable coroutine.
- **GC-safe** — environment reference cycles (e.g. a recursive closure
  stored in its own captured environment) are collected by Python's
  cyclic GC; covered by weakref tests.
- **Source positions** — tokens and AST nodes carry UTF-8 *byte* spans.

## Language

S-expression syntax. Special forms:

```
(do e ...)                 (let name init)            (set name e)
(fn (a b) body ...)        (def name (a b) body ...)  ; recursive
(if c t [e])               (while c body ...)         (and a b ...) (or a b ...)
(each x lst body ...)      ; fresh binding per iteration
(try body ... [(catch e h ...)] [(finally f ...)])
(throw e)                  (yield [e])
```

Builtins (host functions): `+ - * == != < <= > >= not len list
list-get list-set! push print`. Hosts may inject more via
`Machine(program, globals={...})`.

## Quick start

```python
from minivm import DONE, Machine, compile_program, snapshot

program = compile_program("""
(def make-counter ()
  (let n 0)
  (list (fn () (set n (+ n 1)) n) (fn () n)))
(let c (make-counter))
(let inc (list-get c 0))
(let get (list-get c 1))
(inc)
(yield (get))
(inc) (inc)
(get)
""")

co = Machine(program).start()
assert co.resume() == 1

data = snapshot.dumps(co)                    # JSON string
restored = snapshot.restore(Machine(program), data)
assert restored.resume() == DONE and restored.result == 3
```

## Layout

- `minivm/lexer.py` — tokenizer, UTF-8 byte spans
- `minivm/parser.py` — AST
- `minivm/compiler.py` — bytecode compiler, program digest
- `minivm/runtime.py` — VM, coroutines, limits, builtins
- `minivm/snapshot.py` — snapshot serialize/restore + validation
- `examples/` — runnable demos (`python3.11 examples/run_examples.py`)
- `tests/` — unittest suite

## Tests & demos

```
python3.11 -m unittest discover -s tests -t . -v
python3.11 examples/run_examples.py
```
