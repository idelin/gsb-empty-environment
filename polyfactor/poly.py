"""Exact multivariate polynomials over Q (standard library only).

A polynomial is stored sparsely as {monomial: Fraction} where a monomial is
a tuple of non-negative integer exponents aligned with a fixed variable
order.  The canonical form stores no zero coefficients, so equality of two
canonical polynomials is exact structural equality -- the independent
verification path relies on this.
"""
from __future__ import annotations

import re
from fractions import Fraction
from math import gcd, lcm


class Poly:
    """Sparse multivariate polynomial with exact rational coefficients."""

    __slots__ = ("variables", "terms")

    def __init__(self, variables, terms=None):
        self.variables = tuple(variables)
        self.terms = {}
        if terms:
            for mono, coeff in terms.items():
                coeff = Fraction(coeff)
                if coeff:
                    self.terms[tuple(mono)] = coeff

    # -- constructors ----------------------------------------------------
    @classmethod
    def zero(cls, variables):
        return cls(variables)

    @classmethod
    def constant(cls, variables, value):
        value = Fraction(value)
        terms = {(0,) * len(variables): value} if value else {}
        return cls(variables, terms)

    @classmethod
    def var(cls, variables, name):
        mono = [0] * len(variables)
        mono[variables.index(name)] = 1
        return cls(variables, {tuple(mono): Fraction(1)})

    @classmethod
    def monomial(cls, variables, coeff, mono):
        return cls(variables, {tuple(mono): Fraction(coeff)})

    # -- basics ----------------------------------------------------------
    def is_zero(self):
        return not self.terms

    def key(self):
        return tuple(sorted(self.terms.items()))

    def __eq__(self, other):
        return self.variables == other.variables and self.terms == other.terms

    def __hash__(self):
        return hash((self.variables, self.key()))

    # -- exact canonical arithmetic --------------------------------------
    def __add__(self, other):
        terms = dict(self.terms)
        for mono, coeff in other.terms.items():
            terms[mono] = terms.get(mono, Fraction(0)) + coeff
        return Poly(self.variables, terms)

    def __neg__(self):
        return Poly(self.variables, {m: -c for m, c in self.terms.items()})

    def __sub__(self, other):
        return self + (-other)

    def __mul__(self, other):
        terms = {}
        for m1, c1 in self.terms.items():
            for m2, c2 in other.terms.items():
                mono = tuple(a + b for a, b in zip(m1, m2))
                terms[mono] = terms.get(mono, Fraction(0)) + c1 * c2
        return Poly(self.variables, terms)

    # -- content / primitive part ----------------------------------------
    def content(self):
        """Return (c, m): the rational gcd of the coefficients and the
        minimum-exponent monomial, so that self = c * x^m * Q exactly.

        The rational gcd of {n_i / d_i} is gcd(n_i) / lcm(d_i): the greatest
        rational dividing every coefficient.  Zero polynomial is a special
        case and has no content (raises ValueError).
        """
        if not self.terms:
            raise ValueError("the zero polynomial has no content")
        num = 0
        den = 1
        for coeff in self.terms.values():
            num = gcd(num, coeff.numerator)
            den = lcm(den, coeff.denominator)
        c = Fraction(num, den)
        n = len(self.variables)
        m = tuple(min(mono[i] for mono in self.terms) for i in range(n))
        return c, m

    def divide_by_content(self, c, m):
        """Exact division by c * x^m; coefficients stay exact rationals."""
        terms = {}
        for mono, coeff in self.terms.items():
            reduced = tuple(a - b for a, b in zip(mono, m))
            terms[reduced] = coeff / c
        return Poly(self.variables, terms)

    # -- printing ----------------------------------------------------------
    def __str__(self):
        if not self.terms:
            return "0"
        parts = []
        for mono, coeff in sorted(self.terms.items()):
            body = _format_term(abs(coeff), mono, self.variables)
            if not parts:
                parts.append(("-" if coeff < 0 else "") + body)
            else:
                parts.append((" - " if coeff < 0 else " + ") + body)
        return "".join(parts)

    def __repr__(self):
        return f"Poly({self.variables!r}, {str(self)!r})"


def _format_term(coeff, mono, variables):
    factors = []
    if coeff != 1 or not any(mono):
        factors.append(str(coeff))
    for name, exp in zip(variables, mono):
        if exp == 1:
            factors.append(name)
        elif exp > 1:
            factors.append(f"{name}^{exp}")
    return "*".join(factors)


# ---------------------------------------------------------------------------
# Parser:  expr := term (('+'|'-') term)*
#          term := factor ('*' factor)*
#          factor := unary ('^' nonneg-int)?
#          unary := ('-'|'+') unary | atom
#          atom := number | variable | '(' expr ')'
# Numbers are integers or exact ratios p/q.  Exponents must be non-negative
# integer literals: negative exponents would introduce a domain restriction
# (variable != 0) and are rejected explicitly.
# ---------------------------------------------------------------------------
_TOKEN_RE = re.compile(r"\s*(?:(\d+/\d+)|(\d+)|([A-Za-z_]\w*)|(.))")


class _Parser:
    def __init__(self, text, variables):
        self.tokens = []
        for match in _TOKEN_RE.finditer(text):
            frac, num, name, other = match.groups()
            if frac:
                self.tokens.append(("num", Fraction(frac)))
            elif num:
                self.tokens.append(("num", Fraction(int(num))))
            elif name:
                self.tokens.append(("var", name))
            elif other in "+-*^()":
                self.tokens.append((other, other))
            else:
                raise ValueError(f"unexpected character: {other!r}")
        self.tokens.append(("end", None))
        self.pos = 0
        self.variables = variables

    def peek(self):
        return self.tokens[self.pos]

    def next(self):
        tok = self.tokens[self.pos]
        self.pos += 1
        return tok

    def parse_expr(self):
        result = self.parse_term()
        while self.peek()[0] in ("+", "-"):
            op = self.next()[0]
            rhs = self.parse_term()
            result = result + rhs if op == "+" else result - rhs
        return result

    def parse_term(self):
        result = self.parse_factor()
        while self.peek()[0] == "*":
            self.next()
            result = result * self.parse_factor()
        return result

    def parse_factor(self):
        base = self.parse_unary()
        if self.peek()[0] == "^":
            self.next()
            kind, value = self.next()
            if kind != "num" or value.denominator != 1 or value < 0:
                raise ValueError(
                    "exponent must be a non-negative integer literal; "
                    "negative exponents would restrict the domain and are rejected"
                )
            base = _pow(base, int(value))
        return base

    def parse_unary(self):
        kind, _ = self.peek()
        if kind == "-":
            self.next()
            return -self.parse_unary()
        if kind == "+":
            self.next()
            return self.parse_unary()
        return self.parse_atom()

    def parse_atom(self):
        kind, value = self.next()
        if kind == "num":
            return Poly.constant(self.variables, value)
        if kind == "var":
            if value not in self.variables:
                raise ValueError(f"unknown variable: {value!r}")
            return Poly.var(self.variables, value)
        if kind == "(":
            result = self.parse_expr()
            if self.next()[0] != ")":
                raise ValueError("missing closing parenthesis")
            return result
        raise ValueError(f"unexpected token: {value!r}")


def _pow(poly, k):
    result = Poly.constant(poly.variables, 1)
    base = poly
    while k:
        if k & 1:
            result = result * base
        base = base * base
        k >>= 1
    return result


def parse(text, variables=None):
    """Parse a polynomial string into canonical form.

    If `variables` is omitted, variable names are collected from the text
    and sorted.  Multiplication must be explicit ('*').
    """
    if variables is None:
        variables = tuple(sorted(set(re.findall(r"[A-Za-z_]\w*", text))))
    parser = _Parser(text, tuple(variables))
    result = parser.parse_expr()
    if parser.peek()[0] != "end":
        raise ValueError(f"unexpected trailing input: {parser.peek()[1]!r}")
    return result
