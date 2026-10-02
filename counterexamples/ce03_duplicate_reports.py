#!/usr/bin/env python3
"""Counterexample: naive recovery re-reports the same position and never
makes progress; our recovery reports each input position at most once and
always terminates.

Run: python3 counterexamples/ce03_duplicate_reports.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from errparse.demo_grammar import lex, make_parser

SRC = "print ( x ;"  # missing RPAREN
tokens = lex(SRC)
types = [t.type for t in tokens]

# Naive terminal matcher: on mismatch it reports but neither consumes input
# nor pops the expected symbol, so it re-reports the same position forever
# (capped here at 5 iterations so the demo terminates).
naive_positions = []
i = 0
for sym in ["PRINT", "LPAREN", "ID", "RPAREN", "SEMI"]:
    while i < len(types) and types[i] != sym:
        naive_positions.append(i)
        if len(naive_positions) >= 5:
            break
    else:
        i += 1
        continue
    break
print(f"naive recovery reports positions: {naive_positions} (same pos repeated)")

parser = make_parser()
res = parser.parse(tokens)
positions = [e.pos for e in res.errors]
print(f"FOLLOW-sync reports positions:    {positions}")
for e in res.errors:
    print(f"  pos {e.pos}: {e.detail}")
assert len(positions) == len(set(positions)), "duplicate positions reported!"
print("OK: each position reported at most once, parser terminated")
