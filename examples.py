"""Sample programs demonstrating the verifier.  Run: python3 examples.py"""
from __future__ import annotations

from analyzer import Analyzer
from concrete import confirm, replay
from model import (Alias, BorrowMut, BorrowShared, Call, Close, EndBorrow,
                   Function, If, Move, Open, Read, Return, While, Write,
                   program)


def show(name, prog, **kw):
    rep = Analyzer(prog, **kw).analyze()
    if rep.errors:
        confirm(prog, rep)
    print("[%s] %s" % (rep.verdict, name))
    for e in rep.errors:
        print("    - %s: %s" % (e.kind, e.detail))
        if e.concrete_trace is not None:
            kind, snap = replay(prog, e.concrete_trace)
            assert kind == e.kind, (kind, e.kind)
            print("      replayable counterexample confirmed; "
                  "concrete state: %s" % (snap,))
        else:
            print("      possible only (no concrete witness found); "
                  "abstract state: %s" % e.state.pretty())
    if rep.pending_reason:
        print("    pending: %s" % rep.pending_reason)
    print()
    return rep


def main():
    # 1. plain use-after-close
    show("use after close",
         program([Open("x"), Close("x"), Read("x")]))

    # 2. only one branch closes; merged use afterwards -> possible error
    show("close on one branch, use after merge",
         program([Open("x"), If([Close("x")], []), Read("x")]))

    # 3. shared borrows allow reads, forbid writes; EndBorrow restores
    show("shared borrow: read ok, owner write forbidden",
         program([Open("x"), BorrowShared("x", "r"), Read("r"), Write("x")]))
    show("shared borrow returned: owner write ok",
         program([Open("x"), BorrowShared("x", "r"), Read("r"),
                  EndBorrow("r"), Write("x"), Close("x")]))

    # 4. exclusive borrow excludes everyone else
    show("mut borrow excludes owner read",
         program([Open("x"), BorrowMut("x", "w"), Read("x")]))
    show("mut borrow excludes second mut borrow",
         program([Open("x"), BorrowMut("x", "w"), BorrowMut("x", "v")]))

    # 5. close while borrowed
    show("close while borrowed",
         program([Open("x"), BorrowShared("x", "r"), Close("x")]))

    # 6. move then use
    show("use after move",
         program([Open("x"), Move("y", "x"), Read("x")]))
    show("move then use new owner is fine",
         program([Open("x"), Move("y", "x"), Write("y"), Close("y")]))

    # 7. aliases share one resource identity (not two independent handles)
    show("alias: close via one alias, use via the other",
         program([Open("x"), Alias("y", "x"), Close("y"), Read("x")]))

    # 8. cross-function transfer via summaries
    shut = Function("shut", ("h",), [Close("h")])
    show("callee closes the handle, caller uses it",
         program([Open("x"), Call("shut", ("x",)), Read("x")], shut))
    give = Function("give", (), [Open("g"), Return("g")])
    show("handle returned across functions, then closed",
         program([Call("give", (), "x"), Close("x"), Write("x")], give))

    # 9. recursion solved to a fixpoint
    rec = Function("rec", ("h",), [If([Close("h")], [Call("rec", ("h",))])])
    show("recursive closer (fixpoint), then use",
         program([Open("x"), Call("rec", ("x",)), Read("x")], rec))

    # 10. loops
    show("loop of writes, close after: safe",
         program([Open("x"), While([Write("x")]), Close("x")]))
    show("close inside loop: double-close",
         program([Open("x"), While([Close("x")])]))

    # 11. budget exhausted -> verdict stays PENDING, never claims safety
    show("tiny step budget stays pending",
         program([Open("x"), Close("x"), Read("x")]), max_steps=1)


if __name__ == "__main__":
    main()
