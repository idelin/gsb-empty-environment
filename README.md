# grammar-compiler

An incremental grammar compiler in pure Python 3.11 (standard library only).
It computes nullable / FIRST / FOLLOW sets and the LL(1) predictive table
with full provenance tracking, supports namespaced modules and restricted
conditional productions, and maintains every derived set element
incrementally as rules are added/removed and conditions are toggled.

## Layout

- `grammar_compiler/symbols.py` — namespaced symbols (`module.name`) and productions
- `grammar_compiler/engine.py` — incremental proof engine (DRed-style retraction)
- `grammar_compiler/reference.py` — independent from-scratch fixpoint reference
- `grammar_compiler/table.py` — predictive table, shortest reachable prefixes, conflict reports
- `grammar_compiler/parser.py` — table-driven parser (conflict + left-recursion safe)
- `grammar_compiler/lexer.py` — explicit-DFA lexer (alphabet/transitions as data)
- `grammar_compiler/snapshot.py` — immutable compile snapshots with `verify()`
- `grammar_compiler/grammar.py` — modules, imports, conditions, transactions, `compile()`
- `examples/` — runnable demos; `tests/` — unittest suite

## Key properties

- **Provenance**: every set element keeps all supporting proofs
  (`analysis.proofs_of(fact)`, `analysis.explain(fact)`). When the last
  supporting evidence disappears the element is withdrawn; cyclic proof
  chains are handled by over-delete + re-derive (DRed), so left-recursive or
  mutually recursive rules never produce stale facts or infinite loops.
- **Namespaces**: nonterminals are `(module, name)` pairs; two modules may
  define `Expr` independently; cross-module references use `mod.Name` or
  import aliases.
- **Conditions**: `module.add(..., condition="ext")` registers a restricted
  production that only participates while `enable_condition("ext")` is set.
- **Conflicts**: a conflict report names the nonterminal/terminal cell, the
  shortest reachable terminal prefix (Dijkstra over leftmost derivations),
  the competing productions and why each predicts the terminal. The witness
  is replayable through the parser (`examples/conflict_demo.py`).
- **Snapshots**: `grammar.compile()` freezes sets/table/conflicts;
  `snapshot.verify()` recomputes everything from the stored productions.
  `grammar.transaction()` rolls back all state on failure.

## Run

```sh
python3.11 -m unittest discover -s tests   # 28 tests
python3.11 -m examples.arithmetic_demo     # incremental demo
python3.11 -m examples.conflict_demo       # standalone counterexample
```

## Example

```python
from grammar_compiler import Grammar, Parser

g = Grammar()
g.add_terminals("if", "then", "else", "print", "x")
lang = g.module("lang")
lang.add("S", ["if", "E", "then", "S", "S2"])
lang.add("S", ["print"])
lang.add("S2", ["else", "S"])
lang.add("S2", [])
lang.add("E", ["x"])
g.set_start("lang", "S")

snap = g.compile()
print(snap.conflicts[0])
# CONFLICT at lang.S2 on lookahead 'else'
#   shortest reachable prefix: if x then print
#   witness input: if x then print else
#   competing #1 lang.S2 -> 'else' lang.S   [predicted via FIRST of rhs]
#   competing #2 lang.S2 -> epsilon   [predicted via FOLLOW of lhs (nullable rhs)]
```
