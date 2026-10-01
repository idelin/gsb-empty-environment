"""Runnable demo: namespaced modules, conditional productions, incremental
updates, snapshots and parsing of a real token stream.

Run from the repository root:
    python -m examples.arithmetic_demo
"""
from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from grammar_compiler import DFA, Grammar, Lexer, Parser, literal_dfa


def build_lexer():
    digits = "0123456789"
    transitions = {}
    for d in digits:
        transitions[("start", d)] = "num"
        transitions[("num", d)] = "num"
    number = DFA(set(digits), transitions, "start", {"num"})
    lexer = Lexer()
    lexer.add_token("NUM", number)
    lexer.add_token("PLUS", literal_dfa("+"))
    lexer.add_token("STAR", literal_dfa("*"))
    lexer.add_token("LP", literal_dfa("("))
    lexer.add_token("RP", literal_dfa(")"))
    return lexer


def build_grammar():
    g = Grammar()
    g.add_terminals("NUM", "PLUS", "STAR", "LP", "RP")
    expr = g.module("expr")
    expr.add("E", ["T", "E2"])
    expr.add("E2", ["PLUS", "T", "E2"])
    expr.add("E2", [])
    expr.add("T", ["NUM"])
    expr.add("T", ["LP", "E", "RP"])
    # restricted conditional production: STAR is an optional extension
    expr.add("E2", ["STAR", "T", "E2"], condition="ext")
    g.set_start("expr", "E")
    return g


def main():
    g = build_grammar()
    snap1 = g.compile()
    print("== base grammar (condition 'ext' disabled) ==")
    print("nullable:", sorted(str(s) for s in snap1.nullable))
    print("conflicts:", len(snap1.conflicts))
    snap1.verify()
    print("snapshot rev", snap1.revision, "verified")

    parser = Parser(snap1.table_dict(), snap1.production_map(), snap1.start)
    tokens = build_lexer().tokenize("2 + ( 3 + 4 )")
    print("tokens:", tokens)
    print("parse steps:", parser.parse(tokens))

    # flip the condition on: incremental update, old snapshot stays valid
    g.enable_condition("ext")
    snap2 = g.compile()
    print("\n== after enabling 'ext' (incremental) ==")
    print("revision:", snap1.revision, "->", snap2.revision)
    snap2.verify()
    snap1.verify()
    print("both snapshots verified")
    parser2 = Parser(snap2.table_dict(), snap2.production_map(), snap2.start)
    tokens2 = build_lexer().tokenize("2 * 3 + 4")
    print("tokens:", tokens2)
    print("parse steps:", parser2.parse(tokens2))

    # provenance of a FIRST element
    from grammar_compiler.symbols import Sym
    fact = ("first", Sym("N", "E", "expr"), Sym("T", "NUM"))
    print("\nproofs of FIRST(expr.E) containing 'NUM':")
    for line in g.analysis.explain(fact):
        print("  ", line)


if __name__ == "__main__":
    main()
