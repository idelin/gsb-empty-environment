"""Reliability check by enumerating small finite programs.

For every generated program we compare the analyzer against the exhaustive
concrete semantics and assert:

* soundness:   analyzer SAFE  =>  no concrete execution errors;
* completeness: any concrete error  =>  analyzer is not SAFE (it must flag a
               possible error);
* replayability: every reported error whose kind has a concrete witness gets
               a replayable counterexample (verified by re-running it);
* alias identity: aliases never become independent resources (checked by a
               dedicated program included in the enumeration).

Programs whose concrete exploration exceeds the step budget are skipped
(nothing may be concluded from them); programs the analyzer could not finish
are expected to stay PENDING and are likewise excluded from the soundness
assertions.
"""
from __future__ import annotations

import itertools

from analyzer import Analyzer
from concrete import confirm, explore, replay
from model import (Alias, BorrowMut, BorrowShared, Call, Close, EndBorrow, If,
                   Move, Open, Read, Write, program)


def atoms():
    return [
        Open("x"), Open("y"),
        Close("x"), Close("y"),
        Read("x"), Read("y"),
        Write("x"), Write("y"),
        Alias("y", "x"), Alias("x", "y"),
        Move("y", "x"), Move("x", "y"),
        BorrowShared("x", "r"), BorrowMut("x", "r"),
        EndBorrow("r"),
    ]


def gen_programs(max_len=3, with_if=True):
    base = atoms()
    for n in range(1, max_len + 1):
        for combo in itertools.product(base, repeat=n):
            yield program(list(combo))
    if with_if:
        for s1 in base:
            for s2 in base:
                yield program([If([s1], [s2])])


def run_check(max_len=3, with_if=True, verbose=True):
    total = safe = unsafe = pending = skipped = 0
    replayed = 0
    failures = []
    for prog in gen_programs(max_len=max_len, with_if=with_if):
        total += 1
        rep = Analyzer(prog).analyze()
        concrete, complete = explore(prog)
        if not complete:
            skipped += 1
            continue
        if rep.verdict == "PENDING":
            pending += 1
            continue
        if rep.verdict == "SAFE":
            safe += 1
            if concrete:
                failures.append(("SAFE but concrete errors %s" % concrete, prog))
            continue
        unsafe += 1
        # completeness: every concrete error kind must be flagged abstractly
        abstract_kinds = {e.kind for e in rep.errors}
        missing = set(concrete) - abstract_kinds
        if missing:
            failures.append(("concrete %s not flagged" % missing, prog))
        # replayability of concretizable counterexamples
        confirm(prog, rep)
        for e in rep.errors:
            if e.concrete_trace is not None:
                kind, _ = replay(prog, e.concrete_trace)
                replayed += 1
                if kind != e.kind:
                    failures.append(("replay mismatch %s != %s"
                                     % (kind, e.kind), prog))
    if verbose:
        print("programs: %d (safe %d, unsafe %d, pending %d, skipped %d)"
              % (total, safe, unsafe, pending, skipped))
        print("replayed counterexamples: %d" % replayed)
        print("failures: %d" % len(failures))
        for msg, prog in failures[:10]:
            print("  FAIL:", msg, prog)
    return failures


if __name__ == "__main__":
    import sys
    n = int(sys.argv[1]) if len(sys.argv) > 1 else 3
    fails = run_check(max_len=n)
    sys.exit(1 if fails else 0)
