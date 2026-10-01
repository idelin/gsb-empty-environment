"""fsv: a finite-state verifier for a small restricted imperative language.

- Explicit bounded integer domain; overflow is either an error or wraps
  (modular arithmetic), selectable per run.
- Programs (assignments, conditionals, loops, try/finally, arrays on a
  finite heap with aliasing) are unrolled into a finite state graph.
- State normalization preserves sharing identity (aliasing), it never
  collapses distinct reference structures by sorting values.
- Every exploration edge carries per-step evidence anchored at a source
  location; counterexamples are replayed by an independent interpreter.
- Verification is only about the explored model: incomplete exploration
  yields UNKNOWN, never a success verdict.
"""

from .domain import ERROR, WRAP, IntDomain
from .executor import Evidence, initial_state, step
from .explore import Graph, explore
from .faults import Fault, FaultError
from .lang import (
    Assert,
    Assign,
    Bin,
    If,
    Idx,
    Len,
    Lit,
    NewArr,
    Store,
    Try,
    Un,
    Var,
    While,
    arrlen,
    assert_,
    assign,
    bin,
    iff,
    idx,
    lit,
    neg,
    newarr,
    not_,
    store,
    try_,
    var,
    while_,
)
from .replay import interpret, replay_counterexample
from .state import Ref, State, normalize, summarize
from .verify import SAFE, UNKNOWN, UNSAFE, Verdict, verify

__all__ = [
    "ERROR", "WRAP", "IntDomain",
    "Evidence", "initial_state", "step",
    "Graph", "explore",
    "Fault", "FaultError",
    "Assert", "Assign", "Bin", "If", "Idx", "Len", "Lit", "NewArr", "Store",
    "Try", "Un", "Var", "While",
    "arrlen", "assert_", "assign", "bin", "iff", "idx", "lit", "neg",
    "newarr", "not_", "store", "try_", "var", "while_",
    "interpret", "replay_counterexample",
    "Ref", "State", "normalize", "summarize",
    "SAFE", "UNKNOWN", "UNSAFE", "Verdict", "verify",
]
