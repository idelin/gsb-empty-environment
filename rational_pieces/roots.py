"""实根隔离（Sturm 序列）与符号测试：全部精确有理数运算。

预算语义：每次区间二分消耗 1 步；预算耗尽时，尚未分离到“恰好一个根”
的区间以 undecided 形式保留，绝不猜测。
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction
from math import gcd as _igcd, isqrt, lcm

from .poly import Poly


@dataclass
class IsolationBudget:
    """根隔离二分步数预算。"""

    max_steps: int = 1024
    used: int = 0

    def try_consume(self, n: int = 1) -> bool:
        if self.used + n > self.max_steps:
            return False
        self.used += n
        return True


def sturm_sequence(p: Poly) -> list[Poly]:
    """p 的 Sturm 序列（p 应非零）。"""
    seq = [p, p.derivative()]
    while not seq[-1].is_zero():
        seq.append(-(seq[-2] % seq[-1]))
    seq.pop()  # 去掉末尾零多项式
    return seq


def _variations(signs) -> int:
    last = 0
    count = 0
    for s in signs:
        if s == 0:
            continue
        if last and s != last:
            count += 1
        last = s
    return count


def _signs_at(seq, x) -> list[int]:
    return [q.sign_at(x) for q in seq]


def _signs_at_inf(seq, positive: bool) -> list[int]:
    out = []
    for q in seq:
        lc = q.leading_coeff()
        s = (lc > 0) - (lc < 0)
        if not positive and q.degree % 2 == 1:
            s = -s
        out.append(s)
    return out


def count_real_roots(p: Poly, lo=None, hi=None) -> int:
    """p 在 (lo, hi] 内不同实根个数（lo/hi 为 None 表示 ∓/±∞）。

    采用无平方部分的 Sturm 序列；端点恰为根时按 (lo, hi] 约定计数。
    """
    if p.is_zero():
        raise ValueError("零多项式有无穷多根")
    sq = p.squarefree_part()
    if sq.degree < 1:
        return 0
    seq = sturm_sequence(sq)
    va = _variations(_signs_at_inf(seq, False)) if lo is None else _variations(_signs_at(seq, lo))
    vb = _variations(_signs_at_inf(seq, True)) if hi is None else _variations(_signs_at(seq, hi))
    return va - vb


def cauchy_bound(p: Poly) -> Fraction:
    """Cauchy 根界：全部实根 |x| <= 1 + max|a_i/a_n|。"""
    if p.degree < 1:
        return Fraction(0)
    an = abs(p.leading_coeff())
    return 1 + max((abs(c) / an for c in p.coeffs[:-1]), default=Fraction(0))


@dataclass
class RootIsolation:
    """根隔离结果。intervals: (a, b] 各含恰好一个根；undecided: 预算耗尽遗留。"""

    intervals: list = field(default_factory=list)   # list[(Fraction, Fraction)]
    undecided: list = field(default_factory=list)   # list[(Fraction, Fraction, int)]
    complete: bool = True

    @property
    def root_count(self) -> int:
        return len(self.intervals)


def isolate_roots(p: Poly, lo=None, hi=None, budget: IsolationBudget | None = None) -> RootIsolation:
    """把 p 在 (lo, hi] 内的实根隔离到互不相交的小区间。

    每个结果区间经 Sturm 计数核验“恰好含一个根”；预算耗尽时未分离
    区间进入 undecided，complete=False。
    """
    if p.is_zero():
        raise ValueError("零多项式有无穷多根")
    sq = p.squarefree_part()
    if sq.degree < 1:
        return RootIsolation()
    if lo is None or hi is None:
        bound = cauchy_bound(sq) + 1
        lo = -bound if lo is None else lo
        hi = bound if hi is None else hi
    total = count_real_roots(sq, lo, hi)
    work = [(lo, hi, total)] if total else []
    isolated, undecided = [], []
    while work:
        a, b, k = work.pop()
        if k == 0:
            continue
        if k == 1:
            isolated.append((a, b))
            continue
        if budget is not None and not budget.try_consume():
            undecided.append((a, b, k))
            continue
        mid = (a + b) / 2
        k_left = count_real_roots(sq, a, mid)
        k_right = count_real_roots(sq, mid, b)
        work.append((a, mid, k_left))
        work.append((mid, b, k_right))
    isolated.sort()
    undecided.sort()
    return RootIsolation(isolated, undecided, complete=not undecided)


def rational_roots(p: Poly) -> list[Fraction]:
    """有理根定理精确枚举 p 的全部有理根（升序）。"""
    if p.is_zero():
        raise ValueError("零多项式")
    sq = p.squarefree_part()
    if sq.degree < 1:
        return []
    den = 1
    for c in sq.coeffs:
        den = lcm(den, c.denominator)
    ints = [int(c * den) for c in sq.coeffs]
    g = 0
    for c in ints:
        g = _igcd(g, abs(c))
    ints = [c // g for c in ints]
    roots: list[Fraction] = []
    while ints and ints[0] == 0:
        roots.append(Fraction(0))
        ints.pop(0)
    if len(ints) >= 2:
        a0, an = ints[0], ints[-1]

        def divisors(n):
            n = abs(n)
            ds = set()
            for i in range(1, isqrt(n) + 1):
                if n % i == 0:
                    ds.add(i)
                    ds.add(n // i)
            return ds

        scaled = Poly(ints)
        for pn in divisors(a0):
            for qn in divisors(an):
                for cand in (Fraction(pn, qn), Fraction(-pn, qn)):
                    if scaled.eval(cand) == 0:
                        roots.append(cand)
    return sorted(set(roots))


def sign_on_interval(p: Poly, a, b, budget: IsolationBudget | None = None):
    """p 在 (a, b) 上的恒定符号（+1/-1）。

    先用 Sturm 计数核验区间内无根，再在中点精确取值；有根则返回 None。
    """
    if count_real_roots(p, a, b) != 0:
        return None
    mid = (a + b) / 2
    s = p.sign_at(mid)
    return s if s != 0 else None
