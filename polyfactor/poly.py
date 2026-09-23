"""稀疏多元多项式：有理数精确系数（Fraction），规范单项式顺序。

不变量：
- terms 按单项式元组升序排列，系数均非零；
- 零多项式为 terms == ()，作为特殊情形单独处理。
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from math import gcd

Monomial = tuple[int, ...]


def _lcm(a: int, b: int) -> int:
    return a // gcd(a, b) * b


def rational_gcd(coeffs) -> Fraction:
    """有理系数的最大公因子：gcd(分子) / lcm(分母)，恒取正值。

    即 Q 上整除意义下的最大正有理数 d，使 d 整除每个系数。
    """
    num, den = 0, 1
    for c in coeffs:
        c = Fraction(c)
        num = gcd(num, abs(c.numerator))
        den = _lcm(den, c.denominator)
    return Fraction(num, den)


def term_str(vars, coeff, mono) -> str:
    parts = []
    for v, e in zip(vars, mono):
        if e == 0:
            continue
        parts.append(v if e == 1 else f"{v}^{e}")
    if coeff == 1 and parts:
        head = ""
    elif coeff == -1 and parts:
        head = "-"
    else:
        head = str(coeff)
    body = "*".join(parts)
    if head and body:
        return f"{head}*{body}"
    return head or body or "1"


@dataclass(frozen=True)
class Polynomial:
    vars: tuple[str, ...]
    terms: tuple[tuple[Monomial, Fraction], ...]

    @staticmethod
    def build(vars, d) -> "Polynomial":
        items = tuple(
            sorted((tuple(m), Fraction(c)) for m, c in d.items() if Fraction(c) != 0)
        )
        return Polynomial(tuple(vars), items)

    @staticmethod
    def zero(vars) -> "Polynomial":
        return Polynomial(tuple(vars), ())

    @staticmethod
    def one(vars) -> "Polynomial":
        return Polynomial.build(vars, {tuple(0 for _ in vars): Fraction(1)})

    @property
    def is_zero(self) -> bool:
        return not self.terms

    @property
    def num_terms(self) -> int:
        return len(self.terms)

    def _check(self, other: "Polynomial") -> None:
        if self.vars != other.vars:
            raise ValueError("变量集不一致")

    def add(self, other: "Polynomial") -> "Polynomial":
        self._check(other)
        d = dict(self.terms)
        for m, c in other.terms:
            v = d.get(m, Fraction(0)) + c
            if v:
                d[m] = v
            else:
                d.pop(m, None)
        return Polynomial(self.vars, tuple(sorted(d.items())))

    def neg(self) -> "Polynomial":
        return Polynomial(self.vars, tuple((m, -c) for m, c in self.terms))

    def sub(self, other: "Polynomial") -> "Polynomial":
        return self.add(other.neg())

    def mul(self, other: "Polynomial") -> "Polynomial":
        self._check(other)
        d: dict[Monomial, Fraction] = {}
        for m1, c1 in self.terms:
            for m2, c2 in other.terms:
                m = tuple(a + b for a, b in zip(m1, m2))
                d[m] = d.get(m, Fraction(0)) + c1 * c2
        return Polynomial.build(self.vars, d)

    def div_term(self, coeff, mono) -> "Polynomial":
        """被单项式 coeff*mono 整除。Q 上系数总可除，只需指数支配。"""
        coeff = Fraction(coeff)
        assert coeff != 0, "除数为零"
        out = {}
        for m, c in self.terms:
            assert all(a >= b for a, b in zip(m, mono)), "不可整除：指数不足"
            out[tuple(a - b for a, b in zip(m, mono))] = c / coeff
        return Polynomial.build(self.vars, out)

    def common_factor(self):
        """全体项的公因子：(有理系数 gcd, 最小指数单项式)。

        零多项式无公因子概念，约定返回 (1, 0向量)，调用方需特判。
        """
        if self.is_zero:
            return Fraction(1), tuple(0 for _ in self.vars)
        g = rational_gcd([c for _, c in self.terms])
        mono = tuple(min(m[i] for m, _ in self.terms) for i in range(len(self.vars)))
        return g, mono

    def display_terms(self):
        """展示顺序：按指数向量字典序降序（x 次数高者优先）。"""
        return sorted(self.terms, key=lambda t: tuple(-e for e in t[0]))

    def __str__(self) -> str:
        if self.is_zero:
            return "0"
        out = []
        for m, c in self.display_terms():
            out.append((c < 0, term_str(self.vars, abs(c), m)))
        text = ("-" if out[0][0] else "") + out[0][1]
        for neg, s in out[1:]:
            text += (" - " if neg else " + ") + s
        return text
