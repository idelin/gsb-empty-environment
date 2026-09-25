"""Lexer and recursive-descent parser for duration/instant expressions.

Grammar (precedence low -> high)::

    expr     := additive ('@' additive)?        # relative base, non-associative
    additive := multiplicative (('+'|'-') multiplicative)*
    multiplicative := unary ('*' unary)*        # one side must be an integer
    unary    := '-' unary | primary
    primary  := literal | '(' expr ')' | wallclock | 'instant(' number ')'
    literal  := (NUMBER UNIT)+                  # units: mo d sd h m s
    wallclock:= DATE 'T' TIME '[' ZONE ']' ('!' TAG)?

The parser enforces a nesting/node budget so exponentially nested input
fails with a precise diagnostic instead of exhausting the machine.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from fractions import Fraction

from .diagnostics import ChronoError, Diagnostic
from .model import MAX_COMPONENT, Duration, WallClock

UNITS = frozenset({"mo", "d", "sd", "h", "m", "s"})
DISAMBIGUATION_TAGS = frozenset({"first", "second", "prev", "next"})


@dataclass(frozen=True)
class Budget:
    max_depth: int = 64
    max_nodes: int = 4096
    max_ops: int = 10_000


@dataclass(frozen=True)
class Token:
    kind: str
    text: str
    line: int
    col: int

    @property
    def length(self) -> int:
        return max(1, len(self.text))


_TOKEN_RE = re.compile(
    r"""
      (?P<WS>[ \t\r\n]+)
    | (?P<DATE>\d{4,}-\d{1,2}-\d{1,2})
    | (?P<TIME>\d{1,2}:\d{2}:\d{2}(?:\.\d+)?)
    | (?P<NUMBER>\d+(?:\.\d+)?(?:/\d+)?)
    | (?P<WORD>[A-Za-z]+)
    | (?P<PUNCT>[+\-*()@\[\]!])
    """,
    re.VERBOSE,
)

_PUNCT_KINDS = {
    "+": "PLUS", "-": "MINUS", "*": "STAR",
    "(": "LPAREN", ")": "RPAREN", "@": "AT",
    "[": "LBRACKET", "]": "RBRACKET", "!": "BANG",
}


def lex(text: str) -> list[Token]:
    tokens: list[Token] = []
    pos = 0
    line = 1
    col = 1
    while pos < len(text):
        match = _TOKEN_RE.match(text, pos)
        if match is None:
            raise ChronoError(
                Diagnostic(f"unexpected character {text[pos]!r}", line, col), text)
        kind = match.lastgroup
        value = match.group()
        if kind != "WS":
            if kind == "PUNCT":
                kind = _PUNCT_KINDS[value]
            elif kind == "WORD":
                kind = "UNIT" if value in UNITS else "IDENT"
            tokens.append(Token(kind, value, line, col))
        for ch in value:
            if ch == "\n":
                line += 1
                col = 1
            else:
                col += 1
        pos = match.end()
    tokens.append(Token("EOF", "", line, col))
    return tokens


# ---------------------------------------------------------------------------
# AST
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Node:
    line: int
    col: int
    length: int


@dataclass(frozen=True)
class Lit(Node):
    value: Duration


@dataclass(frozen=True)
class IntLit(Node):
    value: Fraction


@dataclass(frozen=True)
class WallLit(Node):
    wall: WallClock
    zone: str
    policy: str | None


@dataclass(frozen=True)
class InstantLit(Node):
    seconds: Fraction


@dataclass(frozen=True)
class Add(Node):
    left: Node
    right: Node


@dataclass(frozen=True)
class Sub(Node):
    left: Node
    right: Node


@dataclass(frozen=True)
class Neg(Node):
    operand: Node


@dataclass(frozen=True)
class Scale(Node):
    left: Node
    right: Node


@dataclass(frozen=True)
class At(Node):
    duration: Node
    base: Node


# ---------------------------------------------------------------------------
# Parser
# ---------------------------------------------------------------------------

class Parser:
    def __init__(self, text: str, budget: Budget | None = None):
        self.text = text
        self.budget = budget or Budget()
        self.tokens = lex(text)
        self.pos = 0
        self.depth = 0
        self.nodes = 0

    def parse(self) -> Node:
        node = self._parse_at()
        tok = self._peek()
        if tok.kind != "EOF":
            self._fail(f"unexpected {self._describe(tok)}", tok)
        return node

    # -- helpers -----------------------------------------------------------

    def _peek(self, ahead: int = 0) -> Token:
        return self.tokens[min(self.pos + ahead, len(self.tokens) - 1)]

    def _take(self) -> Token:
        tok = self.tokens[self.pos]
        if tok.kind != "EOF":
            self.pos += 1
        return tok

    def _fail(self, message: str, tok: Token):
        raise ChronoError(
            Diagnostic(message, tok.line, tok.col, tok.length), self.text)

    @staticmethod
    def _describe(tok: Token) -> str:
        return "end of expression" if tok.kind == "EOF" else f"token {tok.text!r}"

    def _expect(self, kind: str, message: str) -> Token:
        tok = self._peek()
        if tok.kind != kind:
            self._fail(message, tok)
        return self._take()

    def _enter(self, tok: Token) -> None:
        self.depth += 1
        if self.depth > self.budget.max_depth:
            self._fail(
                f"nesting budget exceeded: depth {self.depth} exceeds "
                f"limit {self.budget.max_depth}", tok)

    def _exit(self) -> None:
        self.depth -= 1

    def _count_node(self, tok: Token) -> None:
        self.nodes += 1
        if self.nodes > self.budget.max_nodes:
            self._fail(
                f"node budget exceeded: more than {self.budget.max_nodes} "
                f"nodes", tok)

    def _number(self, tok: Token) -> Fraction:
        try:
            return Fraction(tok.text)
        except ZeroDivisionError:
            self._fail("zero denominator in rational number", tok)

    # -- grammar -----------------------------------------------------------

    def _parse_at(self) -> Node:
        left = self._parse_additive()
        if self._peek().kind == "AT":
            tok = self._take()
            base = self._parse_additive()
            if self._peek().kind == "AT":
                self._fail("'@' does not chain; use parentheses", self._peek())
            left = At(tok.line, tok.col, tok.length, left, base)
        return left

    def _parse_additive(self) -> Node:
        node = self._parse_multiplicative()
        while self._peek().kind in ("PLUS", "MINUS"):
            tok = self._take()
            right = self._parse_multiplicative()
            cls = Add if tok.kind == "PLUS" else Sub
            node = cls(tok.line, tok.col, tok.length, node, right)
        return node

    def _parse_multiplicative(self) -> Node:
        node = self._parse_unary()
        while self._peek().kind == "STAR":
            tok = self._take()
            right = self._parse_unary()
            node = Scale(tok.line, tok.col, tok.length, node, right)
        return node

    def _parse_unary(self) -> Node:
        tok = self._peek()
        if tok.kind == "MINUS":
            self._take()
            self._enter(tok)
            operand = self._parse_unary()
            self._exit()
            return Neg(tok.line, tok.col, tok.length, operand)
        return self._parse_primary()

    def _parse_primary(self) -> Node:
        tok = self._peek()
        self._count_node(tok)
        if tok.kind == "LPAREN":
            self._take()
            self._enter(tok)
            node = self._parse_at()
            self._expect("RPAREN", "expected ')'")
            self._exit()
            return node
        if tok.kind == "NUMBER":
            nxt = self._peek(1)
            if nxt.kind == "UNIT":
                return self._parse_literal()
            if nxt.kind == "IDENT":
                self._fail(f"unknown unit {nxt.text!r}", nxt)
            self._take()
            return IntLit(tok.line, tok.col, tok.length, self._number(tok))
        if tok.kind == "DATE":
            return self._parse_wall()
        if tok.kind == "IDENT":
            if tok.text == "instant":
                return self._parse_instant()
            self._fail(f"unknown identifier {tok.text!r}", tok)
        self._fail(
            f"expected a duration, wall clock, or '(' but found "
            f"{self._describe(tok)}", tok)

    def _parse_literal(self) -> Node:
        start = self._peek()
        months = 0
        days = 0
        seconds = Fraction(0)
        end = start
        while self._peek().kind == "NUMBER" and self._peek(1).kind == "UNIT":
            num_tok = self._take()
            unit_tok = self._take()
            end = unit_tok
            value = self._number(num_tok)
            unit = unit_tok.text
            if unit != "s" and value.denominator != 1:
                self._fail(
                    f"fractional quantity only allowed for seconds, "
                    f"not for unit {unit!r}", num_tok)
            if unit == "mo":
                months += int(value)
                self._check_component(months, num_tok)
            elif unit == "d":
                days += int(value)
                self._check_component(days, num_tok)
            elif unit == "sd":
                seconds += value * 86_400
            elif unit == "h":
                seconds += value * 3_600
            elif unit == "m":
                seconds += value * 60
            else:
                seconds += value
            self._check_component(seconds, num_tok)
        length = end.col + end.length - start.col
        return Lit(start.line, start.col, length, Duration(months, days, seconds))

    def _check_component(self, value, tok: Token) -> None:
        if abs(value) > MAX_COMPONENT:
            self._fail(
                f"overflow: component magnitude {abs(value)} exceeds "
                f"limit 2^62", tok)

    def _parse_wall(self) -> Node:
        date_tok = self._take()
        t_tok = self._expect("IDENT", "expected 'T' between date and time")
        if t_tok.text != "T":
            self._fail("expected 'T' between date and time", t_tok)
        time_tok = self._expect("TIME", "expected HH:MM:SS time")
        self._expect("LBRACKET", "expected '[<zone>]' after wall clock time")
        zone_tok = self._expect("IDENT", "expected a zone name")
        self._expect("RBRACKET", "expected ']' after zone name")
        policy = None
        end_tok = zone_tok
        if self._peek().kind == "BANG":
            self._take()
            tag_tok = self._expect("IDENT", "expected a disambiguation tag after '!'")
            if tag_tok.text not in DISAMBIGUATION_TAGS:
                self._fail(
                    f"unknown disambiguation tag '!{tag_tok.text}'; expected "
                    f"one of {sorted(DISAMBIGUATION_TAGS)}", tag_tok)
            policy = tag_tok.text
            end_tok = tag_tok
        try:
            year, month, day = (int(p) for p in date_tok.text.split("-"))
            hour, minute, sec_text = time_tok.text.split(":")
            wall = WallClock(year, month, day, int(hour), int(minute),
                             Fraction(sec_text))
        except ValueError as exc:
            self._fail(f"invalid wall clock: {exc}", date_tok)
        length = end_tok.col + end_tok.length - date_tok.col
        return WallLit(date_tok.line, date_tok.col, length,
                       wall, zone_tok.text, policy)

    def _parse_instant(self) -> Node:
        kw = self._take()
        self._expect("LPAREN", "expected '(' after 'instant'")
        sign = 1
        if self._peek().kind == "MINUS":
            self._take()
            sign = -1
        num_tok = self._expect("NUMBER", "expected a number of seconds")
        close = self._expect("RPAREN", "expected ')'")
        value = sign * self._number(num_tok)
        if abs(value) > MAX_COMPONENT:
            self._fail("overflow: instant magnitude exceeds 2^62 seconds", num_tok)
        return InstantLit(kw.line, kw.col, close.col + 1 - kw.col, value)
