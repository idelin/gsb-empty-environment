"""Runnable samples for the interval analysis toolkit.

Run from the repository root:

    python3 examples/run_samples.py
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), os.pardir))

from interval_analysis.analyzer import Analyzer
from interval_analysis.interval import Interval
from interval_analysis.lang import (
    Assert, Assign, BinOp, Compare, Const, If, Nondet, Var, While, seq,
)
from interval_analysis.verifier import Verifier


def show_state(title, state):
    if state is None:
        print("  %s: unreachable (bottom)" % title)
        return
    print("  %s: %s" % (title, ", ".join(
        "%s = %s" % (k, v) for k, v in sorted(state.items()))))


def sample_analysis():
    print("=" * 72)
    print("1. Abstract interpretation: branch contraction + widening/narrowing")
    print("=" * 72)
    # x = 0;
    # while x < 100:              # loop: widening, then narrowing
    #     x = x + 1
    # d = nondet(-3, 3);
    # if d != 0:                  # straddles zero: cannot drop 0 exactly
    #     y = 100 / d             # possible division by zero (over-approx)
    # z = 1 / (x - 100)           # x = [100,100] here: definite div-by-zero
    program = seq(
        Assign("x", Const(0)),
        While(Compare("<", Var("x"), Const(100)),
              Assign("x", BinOp("+", Var("x"), Const(1))),
              label="count"),
        Assign("d", Nondet(-3, 3)),
        If(Compare("!=", Var("d"), Const(0)),
           Assign("y", BinOp("/", Const(100), Var("d")), label="guarded-div"),
           label="guard"),
        Assign("z", BinOp("/", Const(1), BinOp("-", Var("x"), Const(100))),
               label="unguarded-div"),
    )
    result = Analyzer(widening_delay=1, narrowing_iterations=3).analyze(program)
    print("program exit state:")
    show_state("exit", result.exit_state)
    print("loop fixpoints:")
    for fp in result.fixpoints:
        print("  loop '%s': %d iterations, widening=%s, narrowing=%d, "
              "invariant={%s}" % (
                  fp.label, fp.iterations, fp.widening_applied,
                  fp.narrowing_iterations,
                  ", ".join("%s: %s" % kv for kv in sorted(fp.invariant.items()))))
    print("diagnostics:")
    for diag in result.diagnostics:
        print("  %s" % diag)
    print("precision notes (over-approximations kept sound, not exact):")
    for note in result.precision_notes:
        print("  %s" % note)


def sample_verification():
    print()
    print("=" * 72)
    print("2. Finite-state verification with replayable counterexamples")
    print("=" * 72)

    safe = seq(
        Assign("x", Nondet(0, 3)),
        Assert(Compare(">=", Var("x"), Const(0)), label="nonneg"),
    )
    print("safe model:     %s" % Verifier().verify(safe))

    unsafe = seq(
        Assign("d", Nondet(-1, 2)),
        Assign("y", BinOp("/", Const(10), Var("d")), label="div"),
        Assert(Compare(">", Var("y"), Const(0)), label="pos"),
    )
    result = Verifier().verify(unsafe)
    print("unsafe model:   %s" % result)
    cex = result.counterexample
    replayed = cex.replay(unsafe)
    print("  replay choices=%s -> %s" % (cex.choices, replayed.error))

    unknown = Assign("x", Nondet(0, 100000))
    result = Verifier(max_states=1000).verify(unknown)
    print("budget-limited: %s (verdict stays pending)" % result)


if __name__ == "__main__":
    sample_analysis()
    sample_verification()
