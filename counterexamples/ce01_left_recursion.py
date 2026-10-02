#!/usr/bin/env python3
"""Counterexample: non-LL(1) grammars are rejected at table-construction time.

Run: python3 counterexamples/ce01_left_recursion.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from errparse.grammar import Grammar, TableConflict, build_table


def show(name, start, productions):
    g = Grammar(start, productions)
    try:
        build_table(g)
    except TableConflict as exc:
        print(f"[{name}] rejected as non-LL(1):")
        print(exc)
        return
    print(f"[{name}] UNEXPECTEDLY accepted")
    sys.exit(1)


show("left recursion", "e", {
    "e": [("e", "PLUS", "t"), ("t",)],
    "t": [("NUM",)],
})
show("dangling else", "s", {
    "s": [("IF", "e", "s"), ("IF", "e", "s", "ELSE", "s"), ("a",)],
    "e": [("x",)],
})
print("OK: conflicting grammars never produce a parsing table")
