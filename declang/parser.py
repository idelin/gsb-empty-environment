"""Recursive-descent parser with sync-point recovery.

Grammar:
    program := decl*
    decl    := "let" name "=" expr ";"
    expr    := primary ("+" primary)*
    primary := int | string | name | "(" expr ")"
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import List, Optional, Sequence, Tuple

from .lexer import Diagnostic, Token
from .repair import find_minimal_repairs

MAX_WINDOW = 14  # max tokens considered for one repair region


class ParseError(Exception):
    def __init__(self, index: int, expected: List[str], got: str):
        self.index = index
        self.expected = expected
        self.got = got
        super().__init__(f"expected {expected} at {index}, got {got!r}")


# ---- AST ---------------------------------------------------------------

@dataclass
class IntLit:
    tok: Token

@dataclass
class StrLit:
    tok: Token

@dataclass
class NameRef:
    tok: Token

@dataclass
class Paren:
    lparen: Token
    inner: object
    rparen: Token

@dataclass
class Bin:
    left: object
    op: Token
    right: object

@dataclass
class Decl:
    let_tok: Token
    name_tok: Token
    eq_tok: Token
    expr: object
    semi_tok: Token


# ---- recursive descent -------------------------------------------------

def parse_primary(tokens: Sequence[Token], i: int) -> Tuple[object, int]:
    n = len(tokens)
    if i < n:
        kind = tokens[i].kind
        if kind == "int":
            return IntLit(tokens[i]), i + 1
        if kind == "string":
            return StrLit(tokens[i]), i + 1
        if kind == "name":
            return NameRef(tokens[i]), i + 1
        if kind == "(":
            inner, j = parse_expr(tokens, i + 1)
            if j < n and tokens[j].kind == ")":
                return Paren(tokens[i], inner, tokens[j]), j + 1
            raise ParseError(j, [")"], tokens[j].kind if j < n else "<eof>")
    got = tokens[i].kind if i < n else "<eof>"
    raise ParseError(i, ["int", "string", "name", "("], got)


def parse_expr(tokens: Sequence[Token], i: int) -> Tuple[object, int]:
    left, i = parse_primary(tokens, i)
    while i < len(tokens) and tokens[i].kind == "+":
        op = tokens[i]
        right, i = parse_primary(tokens, i + 1)
        left = Bin(left, op, right)
    return left, i


def parse_decl(tokens: Sequence[Token], i: int = 0) -> Tuple[Decl, int]:
    n = len(tokens)

    def need(kind: str, pos: int) -> Tuple[Token, int]:
        if pos < n and tokens[pos].kind == kind:
            return tokens[pos], pos + 1
        got = tokens[pos].kind if pos < n else "<eof>"
        raise ParseError(pos, [kind], got)

    let_tok, i = need("let", i)
    name_tok, i = need("name", i)
    eq_tok, i = need("=", i)
    expr, i = parse_expr(tokens, i)
    semi_tok, i = need(";", i)
    return Decl(let_tok, name_tok, eq_tok, expr, semi_tok), i


# ---- program driver with recovery --------------------------------------

def _sync_end(tokens: Sequence[Token], i: int) -> int:
    """End (exclusive) of the repair region starting at a failing decl.

    Sync points are ';' (consumed) and the next top-level 'let' (not consumed);
    the window therefore never contains tokens of a following valid decl."""
    j = i
    n = len(tokens)
    while j < n:
        kind = tokens[j].kind
        if kind == ";":
            return j + 1
        if kind == "let" and j > i:
            return j
        j += 1
        if j - i >= MAX_WINDOW:
            return j
    return j


def _op_range(op: tuple) -> Tuple[int, int]:
    tag = op[0]
    if tag == "insert":
        pos = op[1]
        return pos, pos
    tok = op[1]          # delete / replace carry the original token
    return tok.start, tok.end


def _error_prefix(err: ParseError) -> str:
    want = ", ".join(err.expected)
    return f"syntax error: expected {want}, found {err.got}"


def parse_program(
    tokens: Sequence[Token],
    max_cost: int = 2,
    max_candidates: int = 8,
    max_states: int = 60000,
) -> Tuple[List[Decl], List[Diagnostic]]:
    decls: List[Decl] = []
    diags: List[Diagnostic] = []

    i = 0
    n = len(tokens)
    while i < n:
        try:
            node, j = parse_decl(tokens, i)
            decls.append(node)
            i = j
            continue
        except ParseError as exc:
            err = exc  # keep after except-block cleanup

        j = _sync_end(tokens, i)
        window = tuple(tokens[i:j])
        search = find_minimal_repairs(
            window,
            max_cost=max_cost,
            max_candidates=max_candidates,
            max_states=max_states,
        )

        if search.found:
            cand = search.candidates[0]
            node, consumed = parse_decl(cand.tokens, 0)
            assert consumed == len(cand.tokens)
            decls.append(node)

            ranges = [_op_range(op) for op in cand.ops]
            start = min(r0 for r0, _ in ranges)
            end = max(r1 for _, r1 in ranges)
            note = f"recovered by: {cand.describe()}"
            note += f" (proven minimal edit cost {search.cost}"
            note += f", {len(search.candidates)} equal-cost candidate(s)"
            if search.truncated:
                note += "; candidate list truncated"
            note += ")"
            diags.append(
                Diagnostic(
                    f"{_error_prefix(err)}; {note}",
                    start, end, "error",
                    minimal=True,
                    candidates=tuple(c.describe() for c in search.candidates),
                )
            )
            i = j
        else:
            # Greedy panic-mode sync only. This is NOT claimed to be a
            # minimal repair: the edit search exhausted its budget/state cap.
            start = window[0].start
            end = window[-1].end
            why = "state cap reached" if search.truncated else "no repair within budget"
            diags.append(
                Diagnostic(
                    f"{_error_prefix(err)}; {why}; "
                    f"skipped {len(window)} token(s) to synchronize "
                    f"(greedy sync, not a minimal repair)",
                    start, end, "error",
                    minimal=False,
                )
            )
            i = j

    return decls, diags
