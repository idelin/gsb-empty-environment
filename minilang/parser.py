"""Recursive-descent parser with minimal-edit-cost error recovery.

Recovery is a breadth-first search over edit sequences (insert a virtual
token, delete a token, replace a token with a virtual one), each costing 1.
Edits may target the failure point or any already-consumed token of the
current declaration (replacing an earlier token can be strictly cheaper).
Edits beyond the failure point are deferred: they cannot affect the current
failure, so any minimal sequence has an equivalent one in this space.
The first cost level that yields a fully parseable declaration gives the
provably minimal cost, and *all* candidates on that level are returned
(subject to an explicit, reported budget). Greedy panic-mode skipping is
never claimed to be minimal.
"""
from __future__ import annotations

from dataclasses import dataclass

from .ast import BinOp, LetDecl, Num, Paren, Program, Str, Var
from .diagnostics import Diagnostic
from .lexer import decode_string
from .tokens import (EOF, EQ, IDENT, LET, LPAREN, MINUS, NUMBER, PLUS,
                     RPAREN, SEMI, SLASH, STAR, STRING, virtual_token)

_FACTOR_STARTS = frozenset({NUMBER, STRING, IDENT, LPAREN})
_ALPHABET = (LET, IDENT, NUMBER, STRING, EQ, SEMI, PLUS, MINUS, STAR,
             SLASH, LPAREN, RPAREN)


class ParseFailure(Exception):
    def __init__(self, expected, index):
        super().__init__(f"expected {sorted(expected)} at token index {index}")
        self.expected = frozenset(expected)
        self.index = index


class _Parser:
    def __init__(self, toks):
        self.toks = toks
        self.i = 0

    def expect(self, kind):
        tok = self.toks[self.i]
        if tok.kind != kind:
            raise ParseFailure({kind}, self.i)
        self.i += 1
        return tok


def _expr(p):
    left = _term(p)
    while p.toks[p.i].kind in (PLUS, MINUS):
        op = p.toks[p.i]
        p.i += 1
        left = BinOp(op.kind, left, _term(p), op)
    return left


def _term(p):
    left = _factor(p)
    while p.toks[p.i].kind in (STAR, SLASH):
        op = p.toks[p.i]
        p.i += 1
        left = BinOp(op.kind, left, _factor(p), op)
    return left


def _factor(p):
    tok = p.toks[p.i]
    if tok.kind == NUMBER:
        p.i += 1
        return Num(float(tok.text) if "." in tok.text else int(tok.text), tok)
    if tok.kind == STRING:
        p.i += 1
        return Str(decode_string(tok.text), tok)
    if tok.kind == IDENT:
        p.i += 1
        return Var(tok.text, tok)
    if tok.kind == LPAREN:
        p.i += 1
        inner = _expr(p)
        close = p.expect(RPAREN)
        return Paren(tok, inner, close)
    raise ParseFailure(_FACTOR_STARTS, p.i)


def parse_decl_at(toks, start):
    """Parse one declaration starting at index `start`.

    Returns (LetDecl, end_index). Raises ParseFailure on the first mismatch.
    """
    p = _Parser(toks)
    p.i = start
    let_tok = p.expect(LET)
    name_tok = p.expect(IDENT)
    p.expect(EQ)
    expr = _expr(p)
    semi = p.expect(SEMI)
    return LetDecl(let_tok, name_tok, expr, semi), p.i


@dataclass
class Edit:
    op: str  # "insert" | "delete" | "replace"
    kind: str | None  # token kind introduced (insert/replace)
    token: object  # virtual token introduced (insert/replace)
    original: object  # original token removed (delete/replace)


def describe_edits(edits):
    parts = []
    for e in edits:
        if e.op == "insert":
            parts.append(f"insert {e.kind} at byte {e.token.start}")
        elif e.op == "delete":
            parts.append(
                f"delete {e.original.text!r} at bytes "
                f"{e.original.start}..{e.original.end}")
        else:
            parts.append(
                f"replace {e.original.text!r} at bytes "
                f"{e.original.start}..{e.original.end} with {e.kind}")
    return " then ".join(parts)


def recover_decl(tokens, start, *, max_cost=3, max_candidates=16,
                 max_states=20000):
    """Find all minimal-cost edit sequences that make the declaration at
    `start` parse completely (through its terminating SEMI or EOF).

    Returns (solutions, cost, truncated). Each solution is
    (edits, stream, node, end_index, end_orig_index). `cost` is None when no
    recovery exists within `max_cost`; `truncated` is True whenever a budget
    (candidate count or explored states) cut the search short.
    """
    truncated = False
    states = 0
    frontier = [((), list(tokens))]
    for cost in range(0, max_cost + 1):
        solutions = []
        survivors = []
        for edits, stream in frontier:
            states += 1
            if states > max_states:
                truncated = True
                break
            try:
                node, end = parse_decl_at(stream, start)
            except ParseFailure as failure:
                survivors.append((edits, stream, failure))
            else:
                consumed = [t.orig_index for t in stream[start:end]
                            if t.orig_index is not None]
                end_orig = max(consumed) + 1 if consumed else start
                solutions.append((list(edits), stream, node, end, end_orig))
        if solutions:
            if len(solutions) > max_candidates:
                solutions = solutions[:max_candidates]
                truncated = True
            return solutions, cost, truncated
        if cost == max_cost or truncated:
            break
        next_frontier = []
        for edits, stream, failure in survivors:
            limit = min(failure.index, len(stream) - 1)
            cur = stream[limit]
            # Edits at the failure point. Inserting/replacing with a
            # kind that is not expected there fails again immediately,
            # so only expected kinds are useful at minimal cost.
            for kind in sorted(failure.expected):
                vt = virtual_token(kind, cur.start)
                next_frontier.append(
                    (edits + (Edit("insert", kind, vt, None),),
                     stream[:limit] + [vt] + stream[limit:]))
            if cur.kind != EOF:
                next_frontier.append(
                    (edits + (Edit("delete", None, None, cur),),
                     stream[:limit] + stream[limit + 1:]))
                for kind in sorted(failure.expected):
                    vt = virtual_token(kind, cur.start, cur.end,
                                       cur.orig_index)
                    next_frontier.append(
                        (edits + (Edit("replace", kind, vt, cur),),
                         stream[:limit] + [vt] + stream[limit + 1:]))
            # Edits to already-consumed tokens of this declaration:
            # deleting or replacing an earlier token can be strictly
            # cheaper than repairing everything at the failure point.
            for pos in range(start, limit):
                tok = stream[pos]
                next_frontier.append(
                    (edits + (Edit("delete", None, None, tok),),
                     stream[:pos] + stream[pos + 1:]))
                for kind in _ALPHABET:
                    if kind == tok.kind:
                        continue
                    vt = virtual_token(kind, tok.start, tok.end,
                                       tok.orig_index)
                    next_frontier.append(
                        (edits + (Edit("replace", kind, vt, tok),),
                         stream[:pos] + [vt] + stream[pos + 1:]))
        seen = set()
        frontier = []
        for edits, stream in next_frontier:
            key = tuple((t.kind, t.virtual, t.orig_index) for t in stream)
            if key not in seen:
                seen.add(key)
                frontier.append((edits, stream))
    return [], None, truncated


def _fmt_expected(expected):
    return " or ".join(sorted(expected))


def _fmt_found(tok):
    return "end of input" if tok.kind == EOF else repr(tok.text)


def parse_program(tokens, lex_diagnostics=(), *, max_cost=3,
                  max_candidates=16, max_states=20000):
    """Parse a whole program (a list of declarations), recovering from each
    independent error and continuing with the following declarations."""
    decls = []
    diagnostics = list(lex_diagnostics)
    pos = 0
    last = len(tokens) - 1  # index of the EOF token
    while tokens[pos].kind != EOF:
        tok = tokens[pos]
        if tok.kind == SEMI:  # tolerate stray semicolons silently
            pos += 1
            continue
        if tok.kind != LET:
            # One grouped diagnostic for a run of stray tokens; the run
            # stops at the next declaration boundary so valid declarations
            # are never swallowed.
            start = pos
            while tokens[pos].kind not in (LET, SEMI, EOF):
                pos += 1
            diagnostics.append(Diagnostic(
                "unexpected tokens outside a declaration",
                tokens[start].start, tokens[pos - 1].end))
            continue
        try:
            node, end = parse_decl_at(tokens, pos)
        except ParseFailure as failure:
            solutions, cost, truncated = recover_decl(
                tokens, pos, max_cost=max_cost,
                max_candidates=max_candidates, max_states=max_states)
            ftok = tokens[min(failure.index, last)]
            gap_start = (tokens[failure.index - 1].end
                         if failure.index > 0 else ftok.start)
            span = (gap_start, max(gap_start, ftok.start))
            if not solutions:
                reason = ("recovery search exceeded its budget (truncated)"
                          if truncated else
                          f"no recovery within edit cost budget {max_cost}")
                diagnostics.append(Diagnostic(
                    f"expected {_fmt_expected(failure.expected)} before "
                    f"{_fmt_found(ftok)}; {reason}, skipped to next "
                    f"declaration", *span))
                pos += 1
                while tokens[pos].kind not in (LET, EOF):
                    pos += 1
                continue
            # Prefer candidates that resync cleanly onto a declaration
            # boundary; all candidates are still reported.
            solutions.sort(
                key=lambda s: 0
                if tokens[min(s[4], last)].kind in (LET, SEMI, EOF) else 1)
            edits, stream, node, end, end_orig = solutions[0]
            node.edits = edits
            decls.append(node)
            fixes = [describe_edits(candidate[0]) for candidate in solutions]
            message = (f"expected {_fmt_expected(failure.expected)} before "
                       f"{_fmt_found(ftok)}; recovered with minimal edit "
                       f"cost {cost}")
            if truncated:
                message += (f" (showing {len(fixes)} fix candidate(s), "
                            f"list truncated)")
            else:
                message += f" (all {len(fixes)} minimal fix candidate(s) listed)"
            diagnostics.append(Diagnostic(message, *span, cost=cost,
                                          fixes=fixes, truncated=truncated))
            pos = end_orig
            continue
        decls.append(node)
        pos = end
    return Program(decls), diagnostics
