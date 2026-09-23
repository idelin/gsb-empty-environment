"""一元整系数多项式：稀疏表示 + 精确运算 + 项数预算。

不使用任何符号代数库。预算统计的是“一次乘法展开后的项数”，
预算超限时抛出 BudgetExceeded，由上层记为“未知”，绝不猜答案。
"""

from __future__ import annotations

from fractions import Fraction
from math import gcd

from .errors import BudgetExceeded

Budget = int | None


def _check(terms: int, budget: Budget, where: str) -> None:
    if budget is not None and terms > budget:
        raise BudgetExceeded(terms, budget, where)


class Poly:
    """系数只取 int 的一元多项式，coeff[i] 是 x^i 的系数。

    不变量：末尾系数非零；零多项式表示为 coeff == [0]。
    """

    __slots__ = ("coeff",)

    def __init__(self, coeff: list[int]):
        c = list(coeff)
        if not c:
            c = [0]
        while len(c) > 1 and c[-1] == 0:
            c.pop()
        self.coeff = c

    @staticmethod
    def const(value: int) -> "Poly":
        return Poly([int(value)])

    @property
    def degree(self) -> int:
        return 0 if self.coeff == [0] else len(self.coeff) - 1

    @property
    def terms(self) -> int:
        """非零系数的个数。"""
        return sum(1 for a in self.coeff if a != 0)

    @property
    def is_zero(self) -> bool:
        return self.coeff == [0]

    def __eq__(self, other: object) -> bool:
        return isinstance(other, Poly) and self.coeff == other.coeff

    def __hash__(self) -> int:
        return hash(tuple(self.coeff))

    def __repr__(self) -> str:
        return f"Poly({self.coeff!r})"

    def is_const(self) -> bool:
        return len(self.coeff) == 1

    def leading(self) -> int:
        return self.coeff[-1]

    def __neg__(self) -> "Poly":
        return Poly([-a for a in self.coeff])

    def __add__(self, other: "Poly") -> "Poly":
        n = max(len(self.coeff), len(other.coeff))
        c = [0] * n
        for i, a in enumerate(self.coeff):
            c[i] += a
        for i, a in enumerate(other.coeff):
            c[i] += a
        return Poly(c)

    def __sub__(self, other: "Poly") -> "Poly":
        return self + (-other)

    def mul_int(self, k: int) -> "Poly":
        return Poly([a * k for a in self.coeff])

    def __mul__(self, other: "Poly") -> "Poly":
        return multiply(self, other, None)


def multiply(a: Poly, b: Poly, budget: Budget, where: str = "多项式乘法") -> Poly:
    """卷积乘法；完全展开后检查项数预算。"""
    if a.is_zero or b.is_zero:
        return Poly([0])
    c = [0] * (len(a.coeff) + len(b.coeff) - 1)
    for i, ai in enumerate(a.coeff):
        if ai == 0:
            continue
        for j, bj in enumerate(b.coeff):
            if bj != 0:
                c[i + j] += ai * bj
    p = Poly(c)
    _check(p.terms, budget, where)
    return p


def power(p: Poly, n: int, budget: Budget, where: str = "多项式幂") -> Poly:
    """非负整数次幂，二进制快速幂（每次乘法都过预算）。"""
    if n < 0:
        raise ValueError("power 只接受非负整数指数")
    result = Poly([1])
    base = p
    while n:
        if n & 1:
            result = multiply(result, base, budget, where)
        n >>= 1
        if n:
            base = multiply(base, base, budget, where)
    return result


def eval_poly(p: Poly, x: Fraction) -> Fraction:
    """Horner 法在有理数点 x 上精确求值。"""
    value = Fraction(0)
    for a in reversed(p.coeff):
        value = value * x + Fraction(a)
    return value


# ---------------------------------------------------------------------
# 多项式除法与 GCD（欧几里得算法），用于约分；不改变“原始分母”的定义域
# ---------------------------------------------------------------------


def divmod_poly(a: Poly, b: Poly) -> tuple[Poly, Poly]:
    """整系数多项式长除法，返回 Fraction 系数的 (商, 余)。"""
    if b.is_zero:
        raise ZeroDivisionError("多项式除零")
    q: list[Fraction] = [Fraction(0)] * max(0, a.degree - b.degree + 1)
    rem = [Fraction(c) for c in a.coeff]
    lb = Fraction(b.leading())
    while len(rem) - 1 >= b.degree and not all(c == 0 for c in rem):
        shift = len(rem) - 1 - b.degree
        coef = rem[-1] / lb
        q[shift] += coef
        for i, bc in enumerate(b.coeff):
            rem[i + shift] -= coef * bc
        while len(rem) > 1 and rem[-1] == 0:
            rem.pop()
    return Poly([int(c) if c.denominator == 1 else c for c in q]), Poly(rem)


def gcd_poly(a: Poly, b: Poly) -> Poly:
    """多项式最大公因式（符号规范为首项系数为正）。"""
    x, y = a, b
    while not y.is_zero:
        _, r = divmod_poly(x, y)
        if r.is_zero:
            x, y = y, Poly([0])
            break
        # 把余式系数化为互素整数，防止分数膨胀
        lcm = 1
        for c in r.coeff:
            lcm = lcm * c.denominator // gcd(lcm, c.denominator)
        ints = [int(c * lcm) for c in r.coeff]
        g = 0
        for v in ints:
            g = gcd(g, abs(v))
        if g:
            ints = [v // g for v in ints]
        x, y = y, Poly(ints)
    if x.leading() < 0:
        x = -x
    return x


# ---------------------------------------------------------------------
# 有理根（定义域）：有理根定理枚举 + 综合除法重数
# ---------------------------------------------------------------------


def _synthetic_int(p: Poly, root: Fraction) -> tuple[Poly, Fraction]:
    """综合除法除以 (x - root)，返回 (商, 余数)；root 为根时商仍为整系数。"""
    carry = Fraction(0)
    out: list[Fraction] = []
    for a in reversed(p.coeff):
        carry = carry * root + Fraction(a)
        out.append(carry)
    remainder = out[-1]
    q = list(reversed(out[:-1])) or [Fraction(0)]
    return Poly([int(c) for c in q]), remainder


def _divisors(n: int) -> list[int]:
    n = abs(n)
    small: list[int] = []
    d = 1
    while d * d <= n:
        if n % d == 0:
            small.append(d)
            if d * d != n:
                small.append(n // d)
        d += 1
    return small


def rational_roots(p: Poly, budget: Budget = None) -> dict[Fraction, int]:
    """返回 {有理根: 重数}。基于有理根定理，完全精确。

    枚举候选数量本身也受项数预算约束。
    """
    work = p
    roots: dict[Fraction, int] = {}

    # 先剥离零根
    while work.coeff[0] == 0 and not work.is_zero:
        roots[Fraction(0)] = roots.get(Fraction(0), 0) + 1
        work = Poly(work.coeff[1:])

    while work.degree >= 1:
        a0, an = work.coeff[0], work.leading()
        ps = _divisors(a0)
        qs = _divisors(an)
        candidates = {sgn * Fraction(n, d) for n in ps for d in qs for sgn in (1, -1)}
        _check(len(candidates), budget, "有理根候选枚举")
        found: Fraction | None = None
        for cand in sorted(candidates):
            q, rem = _synthetic_int(work, cand)
            if rem == 0:
                found = cand
                # 剥离该根的全部重数
                while rem == 0:
                    roots[cand] = roots.get(cand, 0) + 1
                    work = q
                    _check(work.terms, budget, "除根后商式")
                    if work.degree < 1:
                        break
                    q, rem = _synthetic_int(work, cand)
                break
        if found is None:
            break
    return roots
