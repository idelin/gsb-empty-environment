"""Runnable samples for the fsv finite-state verifier.

Run from the repository root:  python3.11 examples/run_samples.py
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from fsv import (  # noqa: E402
    ERROR,
    WRAP,
    IntDomain,
    assert_,
    assign,
    bin,
    idx,
    newarr,
    store,
    try_,
    var,
    verify,
    while_,
)

DOM_ERR = IntDomain(-8, 7, ERROR)
DOM_WRAP = IntDomain(-8, 7, WRAP)


def show(title, verdict):
    print(f"== {title}")
    print(
        f"   verdict: {verdict.result} "
        f"(states={verdict.explored_states}, steps={verdict.explored_steps})"
    )
    print(f"   {verdict.detail}")
    if verdict.trace:
        for i, t in enumerate(verdict.trace):
            print(f"   {i:3d} [{t.kind:9s}] {t.loc:24s} {t.desc}")
            print(f"        -> {t.state}")
        print(f"   independently replayed: {verdict.replay_ok}")
    print()


def sample_overflow_modes():
    # x := 7; x := x + 1; assert x == -8   over int[-8..7]
    prog = (
        assign("x", 7),
        assign("x", bin("+", "x", 1)),
        assert_(bin("==", "x", -8)),
    )
    show("sample 1a: overflow as error (int[-8..7]/error)", verify(prog, DOM_ERR))
    show("sample 1b: overflow wraps (int[-8..7]/wrap)", verify(prog, DOM_WRAP))


def sample_aliasing():
    prog = (
        newarr("a", 2),
        assign("b", var("a")),          # b aliases a
        store("a", 0, 5),
        assert_(bin("==", idx("b", 0), 5)),
    )
    show("sample 2: aliasing through the finite heap", verify(prog, DOM_ERR))


def sample_finally_cleanup():
    prog = (
        try_(
            (assign("x", 7), assign("x", bin("+", "x", 1))),
            (assign("cleanup_done", 1),),
        ),
    )
    show("sample 3: overflow fault runs finally cleanup", verify(prog, DOM_ERR))


def sample_nontermination_and_budget():
    prog = (assign("x", 0), while_(1, (assign("x", bin("+", "x", 1)),)))
    show(
        "sample 4a: endless loop, wrap mode (finite cycle -> fully explored)",
        verify(prog, DOM_WRAP),
    )
    show(
        "sample 4b: same loop with a tiny state budget (must stay undecided)",
        verify(prog, DOM_WRAP, max_states=4),
    )


if __name__ == "__main__":
    sample_overflow_modes()
    sample_aliasing()
    sample_finally_cleanup()
    sample_nontermination_and_budget()
