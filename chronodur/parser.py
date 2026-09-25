"""Parser, evaluator and compiler for duration expressions.

Grammar (whitespace is insignificant)::

    expr     := ("-" | "+")? sum
    sum      := term (("+" | "-") term)*
    term     := unary ("*" unary)*
    unary    := "(" expr ")" | quantity | number
    quantity := (number unit)+
    number   := INT | INT "." INT | INT "/" INT
    unit     := "mo" | "cd" | "d" | "h" | "min" | "s"

Units: ``mo`` calendar month, ``cd`` calendar day, ``d`` fixed 24-hour day
(exactly 86400 s), ``h``/``min``/``s`` fixed hour/minute/second. Seconds
accept exact rationals (``1/3s``, ``0.5s``).

A leading ``-`` negates the *whole* expression, never a single field, so
``-1h30min`` means ``-(1h 30min)``. Inside an expression only the binary
operators ``+``/``-`` combine terms. All work is bounded by a Budget.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction

from .errors import BudgetExceeded, DurationOverflow, ParseError
from .model import (MAX_CALENDAR_FIELD, MAX_DENOMINATOR, MAX_FIXED_SECONDS,
                    MAX_SCALAR, UNIT_SECONDS, Duration)
from .program import ADD_CAL_DAYS, ADD_FIXED, ADD_MONTHS, Program

UNIT_KIND = {"mo": ADD_MONTHS, "cd": ADD_CAL_DAYS,
             "d": ADD_FIXED, "h": ADD_FIXED, "min": ADD_FIXED, "s": ADD_FIXED}

AMBIGUOUS_UNITS = {
    "D": "ambiguous unit 'D': use 'cd' (calendar day) or 'd' (fixed 24-hour day)",
    "m": "ambiguous unit 'm': use 'mo' (calendar month) or 'min' (minute)",
    "M": "ambiguous unit 'M': use 'mo' (calendar month) or 'min' (minute)",
}


@dataclass
class Budget:
    """Bounds parser work: node visits (steps) and parenthesis depth."""

    max_steps: int = 10_000
    max_depth: int = 100
    _steps_left: int = field(init=False, repr=False)

    def __post_init__(self):
        self._steps_left = self.max_steps


@dataclass(frozen=True)
class _Token:
    kind: str
    text: str
    pos: int


def _tokenize(text: str):
    tokens = []
    i, n = 0, len(text)
    while i < n:
        c = text[i]
        if c.isspace():
            i += 1
            continue
        if c.isdigit():
            j = i
            while j < n and text[j].isdigit():
                j += 1
            if j < n and text[j] == "." and j + 1 < n and text[j + 1].isdigit():
                j += 1
                while j < n and text[j].isdigit():
                    j += 1
            elif j < n and text[j] == "/" and j + 1 < n and text[j + 1].isdigit():
                j += 1
                while j < n and text[j].isdigit():
                    j += 1
            tokens.append(_Token("num", text[i:j], i))
            i = j
            continue
        if c.isalpha():
            j = i
            while j < n and text[j].isalpha():
                j += 1
            tokens.append(_Token("unit", text[i:j], i))
            i = j
            continue
        if c in "+-*()":
            tokens.append(_Token(c, c, i))
            i += 1
            continue
        raise ParseError(f"unexpected character {c!r}", text, i)
    tokens.append(_Token("eof", "", n))
    return tokens


@dataclass(frozen=True)
class _Node:
    kind: str          # num | qty | add | sub | mul | neg
    pos: int
    end: int
    value: object = None
    children: tuple = ()


class _Parser:
    def __init__(self, text, budget=None):
        self.text = text
        self.tokens = _tokenize(text)
        self.index = 0
        self.budget = budget if budget is not None else Budget()
        self.depth = 0

    def peek(self):
        return self.tokens[self.index]

    def advance(self):
        tok = self.tokens[self.index]
        self.index += 1
        return tok

    def spend_step(self, pos):
        self.budget._steps_left -= 1
        if self.budget._steps_left < 0:
            raise BudgetExceeded(
                f"step budget of {self.budget.max_steps} exhausted",
                self.text, pos)

    def parse(self):
        if self.peek().kind == "eof":
            raise ParseError("empty expression", self.text, 0)
        node = self.parse_signed()
        tok = self.peek()
        if tok.kind != "eof":
            raise ParseError(f"unexpected {tok.text!r}", self.text,
                             tok.pos, tok.pos + len(tok.text))
        return node

    def parse_signed(self):
        tok = self.peek()
        if tok.kind == "-":
            self.advance()
            node = self.parse_sum()
            return _Node("neg", tok.pos, node.end, children=(node,))
        if tok.kind == "+":
            self.advance()
            return self.parse_sum()
        return self.parse_sum()

    def parse_sum(self):
        node = self.parse_term()
        while self.peek().kind in ("+", "-"):
            op = self.advance()
            rhs = self.parse_term()
            node = _Node("add" if op.kind == "+" else "sub",
                         node.pos, rhs.end, children=(node, rhs))
        return node

    def parse_term(self):
        node = self.parse_unary()
        while self.peek().kind == "*":
            self.advance()
            rhs = self.parse_unary()
            node = _Node("mul", node.pos, rhs.end, children=(node, rhs))
        return node

    def parse_unary(self):
        self.spend_step(self.peek().pos)
        tok = self.peek()
        if tok.kind == "(":
            self.advance()
            self.depth += 1
            if self.depth > self.budget.max_depth:
                raise BudgetExceeded(
                    f"nesting deeper than budget of {self.budget.max_depth}",
                    self.text, tok.pos)
            node = self.parse_signed()
            closing = self.peek()
            if closing.kind != ")":
                raise ParseError("expected ')'", self.text, closing.pos)
            self.advance()
            self.depth -= 1
            return _Node(node.kind, tok.pos, closing.pos + 1,
                         node.value, node.children)
        if tok.kind == "num":
            return self.parse_quantity()
        if tok.kind == "eof":
            raise ParseError("unexpected end of expression", self.text, tok.pos)
        raise ParseError(f"unexpected {tok.text!r}", self.text,
                         tok.pos, tok.pos + len(tok.text))

    def parse_quantity(self):
        first = self.advance()
        amount = _parse_number(first, self.text)
        if self.peek().kind != "unit":
            return _Node("num", first.pos, first.pos + len(first.text), value=amount)
        terms = [(amount, self.read_unit())]
        while self.peek().kind == "num":
            tok = self.advance()
            amount = _parse_number(tok, self.text)
            if self.peek().kind != "unit":
                raise ParseError("expected a unit after this number",
                                 self.text, tok.pos, tok.pos + len(tok.text))
            terms.append((amount, self.read_unit()))
        last = self.tokens[self.index - 1]
        return _Node("qty", first.pos, last.pos + len(last.text), value=tuple(terms))

    def read_unit(self):
        tok = self.advance()
        unit = tok.text
        if unit in AMBIGUOUS_UNITS:
            raise ParseError(AMBIGUOUS_UNITS[unit], self.text,
                             tok.pos, tok.pos + len(unit))
        if unit not in UNIT_KIND:
            raise ParseError(
                f"unknown unit {unit!r}; expected one of: mo, cd, d, h, min, s",
                self.text, tok.pos, tok.pos + len(unit))
        return unit


def _parse_number(tok, text):
    s = tok.text
    if "/" in s:
        num_s, den_s = s.split("/")
        if int(den_s) == 0:
            raise ParseError("zero denominator in rational number",
                             text, tok.pos, tok.pos + len(s))
        value = Fraction(int(num_s), int(den_s))
    else:
        value = Fraction(s)
    if value.denominator > MAX_DENOMINATOR:
        raise DurationOverflow(
            f"denominator exceeds limit of {MAX_DENOMINATOR}",
            text, tok.pos, tok.pos + len(s))
    return value


# ---------------------------------------------------------------------------
# Value evaluation
# ---------------------------------------------------------------------------

def _check_duration(d, text, node):
    if abs(d.months) > MAX_CALENDAR_FIELD or abs(d.cal_days) > MAX_CALENDAR_FIELD:
        raise DurationOverflow(
            f"calendar field exceeds limit of {MAX_CALENDAR_FIELD}",
            text, node.pos, node.end)
    if abs(d.fixed) > MAX_FIXED_SECONDS:
        raise DurationOverflow(
            f"fixed seconds exceed limit of {MAX_FIXED_SECONDS}",
            text, node.pos, node.end)
    return d


def _check_scalar(value, text, node):
    if abs(value) > MAX_SCALAR:
        raise DurationOverflow(
            f"scalar exceeds limit of {MAX_SCALAR}", text, node.pos, node.end)
    return value


def _term_duration(amount, unit, text, node):
    kind = UNIT_KIND[unit]
    if kind in (ADD_MONTHS, ADD_CAL_DAYS):
        if amount.denominator != 1:
            raise ParseError(
                f"calendar unit {unit!r} requires a whole-number amount",
                text, node.pos, node.end)
        n = int(amount)
        return Duration(months=n) if kind == ADD_MONTHS else Duration(cal_days=n)
    return Duration(fixed=amount * UNIT_SECONDS[unit])


def eval_value(node, text):
    kind = node.kind
    if kind == "num":
        return _check_scalar(node.value, text, node)
    if kind == "qty":
        total = Duration()
        for amount, unit in node.value:
            total = _check_duration(
                total + _term_duration(amount, unit, text, node), text, node)
        return total
    if kind in ("add", "sub"):
        left = eval_value(node.children[0], text)
        right = eval_value(node.children[1], text)
        if isinstance(left, Fraction) and isinstance(right, Fraction):
            value = left + right if kind == "add" else left - right
            return _check_scalar(value, text, node)
        if isinstance(left, Fraction) or isinstance(right, Fraction):
            raise ParseError("cannot mix a bare number with a duration",
                             text, node.pos, node.end)
        value = left + right if kind == "add" else left - right
        return _check_duration(value, text, node)
    if kind == "mul":
        left = eval_value(node.children[0], text)
        right = eval_value(node.children[1], text)
        if isinstance(left, Duration) and isinstance(right, Duration):
            raise ParseError("cannot multiply two durations",
                             text, node.pos, node.end)
        if isinstance(left, Fraction) and isinstance(right, Fraction):
            return _check_scalar(left * right, text, node)
        scalar, dur = (left, right) if isinstance(left, Fraction) else (right, left)
        try:
            scaled = dur.scale(scalar)
        except ValueError as exc:
            raise ParseError(str(exc), text, node.pos, node.end) from exc
        return _check_duration(scaled, text, node)
    if kind == "neg":
        value = eval_value(node.children[0], text)
        if isinstance(value, Fraction):
            return _check_scalar(-value, text, node)
        return _check_duration(-value, text, node)
    raise AssertionError(kind)  # pragma: no cover


# ---------------------------------------------------------------------------
# Program compilation
# ---------------------------------------------------------------------------

def _term_op(amount, unit, text, node):
    kind = UNIT_KIND[unit]
    if kind in (ADD_MONTHS, ADD_CAL_DAYS):
        if amount.denominator != 1:
            raise ParseError(
                f"calendar unit {unit!r} requires a whole-number amount",
                text, node.pos, node.end)
        return (kind, int(amount))
    return (ADD_FIXED, amount * UNIT_SECONDS[unit])


def _negate_ops(ops):
    return tuple((kind, -amount) for kind, amount in ops)


def _scale_ops(ops, scalar, text, node):
    out = []
    for kind, amount in ops:
        scaled = amount * scalar
        if kind in (ADD_MONTHS, ADD_CAL_DAYS):
            if scaled.denominator != 1:
                raise ParseError(
                    "scaling would produce a fractional calendar field",
                    text, node.pos, node.end)
            scaled = int(scaled)
        out.append((kind, scaled))
    return tuple(out)


def _check_ops(ops, text, node):
    for kind, amount in ops:
        if kind in (ADD_MONTHS, ADD_CAL_DAYS) and abs(amount) > MAX_CALENDAR_FIELD:
            raise DurationOverflow(
                f"calendar field exceeds limit of {MAX_CALENDAR_FIELD}",
                text, node.pos, node.end)
        if kind == ADD_FIXED and abs(amount) > MAX_FIXED_SECONDS:
            raise DurationOverflow(
                f"fixed seconds exceed limit of {MAX_FIXED_SECONDS}",
                text, node.pos, node.end)
    return ops


def compile_node(node, text):
    kind = node.kind
    if kind == "num":
        return ("scalar", _check_scalar(node.value, text, node))
    if kind == "qty":
        ops = []
        for amount, unit in node.value:
            op = _term_op(amount, unit, text, node)
            if op[1]:
                ops.append(op)
        return ("ops", _check_ops(tuple(ops), text, node))
    if kind in ("add", "sub"):
        lk, lv = compile_node(node.children[0], text)
        rk, rv = compile_node(node.children[1], text)
        if lk == "scalar" and rk == "scalar":
            return ("scalar", _check_scalar(
                lv + rv if kind == "add" else lv - rv, text, node))
        if lk == "scalar" or rk == "scalar":
            raise ParseError("cannot mix a bare number with a duration",
                             text, node.pos, node.end)
        rhs = rv if kind == "add" else _negate_ops(rv)
        return ("ops", _check_ops(lv + rhs, text, node))
    if kind == "mul":
        lk, lv = compile_node(node.children[0], text)
        rk, rv = compile_node(node.children[1], text)
        if lk == "ops" and rk == "ops":
            raise ParseError("cannot multiply two durations",
                             text, node.pos, node.end)
        if lk == "scalar" and rk == "scalar":
            return ("scalar", _check_scalar(lv * rv, text, node))
        scalar, ops = (lv, rv) if lk == "scalar" else (rv, lv)
        return ("ops", _check_ops(_scale_ops(ops, scalar, text, node), text, node))
    if kind == "neg":
        ck, cv = compile_node(node.children[0], text)
        if ck == "scalar":
            return ("scalar", _check_scalar(-cv, text, node))
        return ("ops", _negate_ops(cv))
    raise AssertionError(kind)  # pragma: no cover


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------

def parse_duration(text: str, budget: Budget | None = None) -> Duration:
    """Parse an expression into an exact, normalized Duration value."""
    node = _Parser(text, budget).parse()
    value = eval_value(node, text)
    if isinstance(value, Fraction):
        raise ParseError("expression is a bare number, not a duration",
                         text, node.pos, node.end)
    return value


def compile_program(text: str, budget: Budget | None = None) -> Program:
    """Compile an expression into an ordered, composable Program.

    Unlike parse_duration, compilation preserves the source order of
    calendar and fixed operations, which do not commute.
    """
    node = _Parser(text, budget).parse()
    kind, value = compile_node(node, text)
    if kind == "scalar":
        raise ParseError("expression is a bare number, not a duration",
                         text, node.pos, node.end)
    return Program(value)
