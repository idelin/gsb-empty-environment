"""有理系数一元多项式：精确代数运算（仅标准库 fractions）。"""
from __future__ import annotations

from fractions import Fraction


class Poly:
    """稠密多项式，系数为 Fraction，下标即次数，无尾部零系数。"""

    __slots__ = ("_coeffs",)

    def __init__(self, coeffs=()):
        cs = tuple(c if isinstance(c, Fraction) else Fraction(c) for c in coeffs)
        n = len(cs)
        while n and cs[n - 1] == 0:
            n -= 1
        self._coeffs = cs[:n]

    # ---- 基本构造 ----
    @staticmethod
    def zero() -> "Poly":
        return Poly(())

    @staticmethod
    def one() -> "Poly":
        return Poly((1,))

    @staticmethod
    def constant(c) -> "Poly":
        return Poly((c,))

    @staticmethod
    def x() -> "Poly":
        return Poly((0, 1))

    @property
    def coeffs(self) -> tuple:
        return self._coeffs

    @property
    def degree(self) -> int:
        return len(self._coeffs) - 1  # 零多项式为 -1

    def is_zero(self) -> bool:
        return not self._coeffs

    def is_constant(self) -> bool:
        return len(self._coeffs) <= 1

    def leading_coeff(self) -> Fraction:
        return self._coeffs[-1] if self._coeffs else Fraction(0)

    @staticmethod
    def _coerce(other) -> "Poly":
        if isinstance(other, Poly):
            return other
        return Poly.constant(other)

    # ---- 算术 ----
    def __add__(self, other):
        other = Poly._coerce(other)
        n = max(len(self._coeffs), len(other._coeffs))
        a = self._coeffs + (Fraction(0),) * (n - len(self._coeffs))
        b = other._coeffs + (Fraction(0),) * (n - len(other._coeffs))
        return Poly([x + y for x, y in zip(a, b)])

    __radd__ = __add__

    def __neg__(self):
        return Poly([-c for c in self._coeffs])

    def __sub__(self, other):
        return self + (-Poly._coerce(other))

    def __rsub__(self, other):
        return Poly._coerce(other) + (-self)

    def __mul__(self, other):
        if isinstance(other, (int, Fraction)):
            return Poly([c * other for c in self._coeffs])
        if not isinstance(other, Poly):
            return NotImplemented
        if self.is_zero() or other.is_zero():
            return Poly.zero()
        out = [Fraction(0)] * (self.degree + other.degree + 1)
        for i, a in enumerate(self._coeffs):
            for j, b in enumerate(other._coeffs):
                out[i + j] += a * b
        return Poly(out)

    def __rmul__(self, other):
        if isinstance(other, (int, Fraction)):
            return self * other
        return NotImplemented

    def divmod(self, other: "Poly"):
        """多项式带余除法，返回 (商, 余式)，系数精确。"""
        if other.is_zero():
            raise ZeroDivisionError("多项式除零")
        if self.degree < other.degree:
            return Poly.zero(), self
        rem = list(self._coeffs)
        quo = [Fraction(0)] * (self.degree - other.degree + 1)
        dd, dlc = other.degree, other.leading_coeff()
        while len(rem) - 1 >= dd and rem:
            k = len(rem) - 1 - dd
            c = rem[-1] / dlc
            quo[k] = c
            for i, oc in enumerate(other._coeffs):
                rem[i + k] -= c * oc
            while rem and rem[-1] == 0:
                rem.pop()
        return Poly(quo), Poly(rem)

    def __floordiv__(self, other):
        return self.divmod(other)[0]

    def __mod__(self, other):
        return self.divmod(other)[1]

    def gcd(self, other: "Poly") -> "Poly":
        """首一最大公因子（欧几里得算法，精确）。"""
        a, b = self, other
        while not b.is_zero():
            a, b = b, a.divmod(b)[1]
        if a.is_zero():
            return Poly.zero()
        return a * (1 / a.leading_coeff())

    def derivative(self) -> "Poly":
        return Poly([i * c for i, c in enumerate(self._coeffs)][1:])

    def squarefree_part(self) -> "Poly":
        """无平方部分 p / gcd(p, p')：保留全部根，重数降为 1。"""
        if self.is_zero() or self.is_constant():
            return self
        d = self.gcd(self.derivative())
        return self // d

    def eval(self, x) -> Fraction:
        x = x if isinstance(x, Fraction) else Fraction(x)
        acc = Fraction(0)
        for c in reversed(self._coeffs):
            acc = acc * x + c
        return acc

    def sign_at(self, x) -> int:
        v = self.eval(x)
        return (v > 0) - (v < 0)

    def deflate(self, x):
        """除以 (t - x)^m，返回 (重数 m, 剩余多项式)。"""
        x = x if isinstance(x, Fraction) else Fraction(x)
        t = Poly([-x, 1])
        m, r = 0, self
        while r.degree >= 1:
            q, rem = r.divmod(t)
            if not rem.is_zero():
                break
            r, m = q, m + 1
        return m, r

    # ---- 比较与展示 ----
    def __eq__(self, other):
        return isinstance(other, Poly) and self._coeffs == other._coeffs

    def __hash__(self):
        return hash(self._coeffs)

    def __str__(self):
        if self.is_zero():
            return "0"
        parts = []
        for i in range(self.degree, -1, -1):
            c = self._coeffs[i]
            if c == 0:
                continue
            a = abs(c)
            if i == 0:
                body = str(a)
            elif i == 1:
                body = "x" if a == 1 else f"{a}*x"
            else:
                body = f"x^{i}" if a == 1 else f"{a}*x^{i}"
            if not parts:
                parts.append(("-" if c < 0 else "") + body)
            else:
                parts.append((" - " if c < 0 else " + ") + body)
        return "".join(parts)

    __repr__ = __str__
