"""A small explicit-DFA language used by the demo, tests and counterexamples.

Tokens: keywords if/while, identifiers, integers, operators
< << <= = == ! !=, whitespace (skipped), and nestable /* ... */ comments
handled through the lexical-state stack.
"""

from __future__ import annotations

import string

from lexer import DFA, Action, Rule, StateOp, charclass_dfa, literal_dfa

ALPHABET = frozenset(string.printable)

_LETTERS = string.ascii_letters + "_"
_ALNUM = _LETTERS + string.digits


def _ident_dfa() -> DFA:
    transitions = {}
    for c in _LETTERS:
        transitions[(0, c)] = 1
    for c in _ALNUM:
        transitions[(1, c)] = 1
    return DFA(ALPHABET, 0, transitions, frozenset({1}))


def build_states() -> dict:
    main = [
        Rule("kw_if", literal_dfa("if", ALPHABET), 0, Action("KW")),
        Rule("kw_while", literal_dfa("while", ALPHABET), 0, Action("KW")),
        Rule("int", charclass_dfa(string.digits, ALPHABET), 5, Action("INT")),
        Rule("ident", _ident_dfa(), 10, Action("IDENT")),
        Rule("op_shift_assign", literal_dfa("<<=", ALPHABET), 5, Action("OP")),
        Rule("op_shift", literal_dfa("<<", ALPHABET), 5, Action("OP")),
        Rule("op_lt", literal_dfa("<", ALPHABET), 5, Action("OP")),
        Rule("op_eq", literal_dfa("==", ALPHABET), 5, Action("OP")),
        Rule("op_assign", literal_dfa("=", ALPHABET), 5, Action("OP")),
        Rule("op_ne", literal_dfa("!=", ALPHABET), 5, Action("OP")),
        Rule("op_not", literal_dfa("!", ALPHABET), 5, Action("OP")),
        Rule("ws", charclass_dfa(" \t\n", ALPHABET), 20, Action(None)),
        Rule("comment_open", literal_dfa("/*", ALPHABET), 1,
             Action(None, StateOp.PUSH, "comment")),
    ]
    comment = [
        Rule("comment_close", literal_dfa("*/", ALPHABET), 1,
             Action(None, StateOp.POP)),
        Rule("comment_nest", literal_dfa("/*", ALPHABET), 1,
             Action(None, StateOp.PUSH, "comment")),
        Rule("comment_body", charclass_dfa(ALPHABET, ALPHABET, repeat=False),
             20, Action(None)),
    ]
    return {"main": main, "comment": comment}
