#!/usr/bin/env python3
"""Counterexample: naive panic-mode recovery swallows legal structure.

`x = 1 y = 2 ;` is missing a SEMI after `1`.  A naive parser that skips to
';' on error eats `y = 2` as garbage.  FOLLOW-synchronised recovery inserts
the missing SEMI and keeps both assignments.

Run: python3 counterexamples/ce02_naive_swallow.py
"""
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from errparse.demo_grammar import lex, make_parser

SRC = "x = 1 y = 2 ;"


def naive_parse(types):
    """Naive statement scan: on a missing ';', skip everything to ';'."""
    assigns = []
    i = 0
    while i < len(types):
        if (types[i] == "ID" and i + 2 < len(types)
                and types[i + 1] == "ASSIGN" and types[i + 2] == "NUM"):
            j = i + 3
            if j < len(types) and types[j] == "SEMI":
                assigns.append(types[i])
                i = j + 1
            else:  # panic: keep the partial statement, skip to SEMI,
                # discarding whatever legal structure sits in between
                assigns.append(types[i])
                while j < len(types) and types[j] != "SEMI":
                    j += 1
                i = j + 1
        else:
            i += 1
    return assigns


tokens = lex(SRC)
types = [t.type for t in tokens]
naive = naive_parse(types)
print(f"naive panic-mode recovered: {naive}  (legal `y = 2` swallowed)")

parser = make_parser()
res = parser.parse(tokens)
assigns = []


def walk(node):
    if (node.symbol == "stmt" and node.kind == "rule" and node.children
            and node.children[0].symbol == "ID"):
        toks = [leaf.token for leaf in node.iter_tokens()]
        assigns.append(" ".join(t.value or f"<{t.type}>" for t in toks))
    for c in node.children:
        walk(c)


walk(res.tree)
print(f"FOLLOW-sync recovered:      {assigns}")
for e in res.errors:
    print(f"  error at pos {e.pos}: {e.detail}")
assert naive == ["ID"], naive
assert any(a.startswith("y") for a in assigns), assigns
print("OK: FOLLOW-sync kept the legal `y = 2 ;` the naive parser swallowed")
