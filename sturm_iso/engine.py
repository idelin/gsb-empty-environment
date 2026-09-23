"""Budget-aware Sturm root isolation with per-root certificates.

Every transformation step documents its applicability domain in NOTES;
the same notes are attached to each IsolationResult.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from fractions import Fraction
from typing import Deque, List, Optional, Tuple

from .polynomials import (ONE, Poly, degree, evaluate, is_zero, mul, poly,
                          squarefree_factorization)
from .sturm import cauchy_bound, sign_variations, sturm_sequence

NOTES: Tuple[str, ...] = (
    "系数域 Q（特征 0）：Q[x] 为欧几里得整环，带余除法与 gcd 精确有效。",
    "形式导数：特征 0 保证非常数多项式导数次数恰减 1，且『重根 <=> gcd(p,p') != 1』成立。",
    "Yun 无平方分解 p = prod f_i^i：在特征 0 的完美域上有效；各 f_i 无平方且两两互素。",
    "无平方部分 q = prod f_i 与 p 具有完全相同的不同实根；重数由因子下标 i 单独记录。",
    "Sturm 序列：要求 q 无平方；对任意 a<b，(a,b] 内不同实根数 = V(a)-V(b)，端点是否为根均适用（左排右含）。",
    "Cauchy 界：任意非零复多项式的根满足 |r| < B；端点非根已用精确求值验证。",
    "二分细化：中点 m 为有理数；若 q(m)=0 则 m 为精确有理根（点根），区间按 (lo,m) 与 (m,hi] 精确拆分，计数守恒。",
    "重数判定：区间根所属因子由逐因子 Sturm 计数唯一确定（两两互素保证恰有一个因子计数为 1）。",
    "预算中断：每次二分消耗一步预算；预算耗尽时返回已隔离根与未决区间（含各自根数证书）。",
)


@dataclass(frozen=True)
class Interval:
    """Half-open region (lo, hi].

    If hi_excluded is set, the region is the open (lo, hi) because hi is
    an exact root that has already been extracted as a point root. Roots
    at lo are never counted inside the interval (left-excluded rule).
    """
    lo: Fraction
    hi: Fraction
    hi_excluded: bool = False


@dataclass
class RootCertificate:
    """Per-root certificate: square-free factor, endpoint sign data, count."""
    root_id: str
    kind: str  # "point" or "interval"
    point: Optional[Fraction]
    interval: Optional[Interval]
    multiplicity: int
    squarefree_factor: Poly
    variations_lo: Optional[int]
    variations_hi: Optional[int]
    signs_lo: Optional[Tuple[int, ...]]
    signs_hi: Optional[Tuple[int, ...]]
    root_count: int = 1


@dataclass
class PendingInterval:
    """An interval whose roots are not yet isolated (budget exhausted)."""
    interval: Interval
    root_count: int
    variations_lo: int
    variations_hi: int


@dataclass
class IsolationResult:
    polynomial: Poly
    squarefree_part: Poly
    squarefree_factors: Tuple[Tuple[Poly, int], ...]
    roots: List[RootCertificate]
    pending: List[PendingInterval]
    completed: bool
    total_distinct_roots: int
    notes: Tuple[str, ...] = NOTES


class SturmEngine:
    """Exact Sturm machinery for one rational polynomial."""

    def __init__(self, p, id_prefix: str = "r"):
        p = poly(p)
        if is_zero(p):
            raise ValueError(
                "zero polynomial: every point is a root; cannot isolate")
        self.p = p
        self.id_prefix = id_prefix
        self.factors: Tuple[Tuple[Poly, int], ...] = squarefree_factorization(p)
        q = ONE
        for f, _ in self.factors:
            q = mul(q, f)
        self.q = q  # square-free part: same distinct real roots as p
        self.seq = sturm_sequence(q)
        self.bound = cauchy_bound(q) if degree(q) > 0 else Fraction(1)
        self._variations_cache = {}
        self._factor_seqs = {}
        self._id_counter = 0

    # -- exact counting -------------------------------------------------
    def variations(self, x) -> Tuple[int, Tuple[int, ...]]:
        x = Fraction(x)
        if x not in self._variations_cache:
            self._variations_cache[x] = sign_variations(self.seq, x)
        return self._variations_cache[x]

    def count_interval(self, iv: Interval) -> int:
        """Distinct real roots of q in the region of iv (exact rule)."""
        if degree(self.q) <= 0:
            return 0
        vlo, _ = self.variations(iv.lo)
        vhi, _ = self.variations(iv.hi)
        return vlo - vhi - (1 if iv.hi_excluded else 0)

    def total_roots(self) -> int:
        if degree(self.q) <= 0:
            return 0
        vlo, _ = self.variations(-self.bound)
        vhi, _ = self.variations(self.bound)
        return vlo - vhi

    # -- certificates ---------------------------------------------------
    def _next_id(self) -> str:
        self._id_counter += 1
        return f"{self.id_prefix}{self._id_counter}"

    def _factor_sequence(self, f: Poly):
        if f not in self._factor_seqs:
            self._factor_seqs[f] = sturm_sequence(f)
        return self._factor_seqs[f]

    def _multiplicity_at_point(self, x: Fraction):
        for f, m in self.factors:
            if evaluate(f, x) == 0:
                return m, f
        raise ValueError(f"{x} is not a root")

    def _multiplicity_in_interval(self, iv: Interval):
        for f, m in self.factors:
            fseq = self._factor_sequence(f)
            vlo, _ = sign_variations(fseq, iv.lo)
            vhi, _ = sign_variations(fseq, iv.hi)
            cnt = vlo - vhi
            if iv.hi_excluded and evaluate(f, iv.hi) == 0:
                cnt -= 1
            if cnt == 1:
                return m, f
        raise ValueError("no square-free factor owns the interval root")

    def point_certificate(self, x, root_id: Optional[str] = None) -> RootCertificate:
        x = Fraction(x)
        mult, factor = self._multiplicity_at_point(x)
        return RootCertificate(
            root_id=root_id or self._next_id(), kind="point", point=x,
            interval=None, multiplicity=mult, squarefree_factor=factor,
            variations_lo=None, variations_hi=None,
            signs_lo=None, signs_hi=None)

    def interval_certificate(self, iv: Interval,
                             root_id: Optional[str] = None) -> RootCertificate:
        vlo, slo = self.variations(iv.lo)
        vhi, shi = self.variations(iv.hi)
        cnt = vlo - vhi - (1 if iv.hi_excluded else 0)
        if cnt != 1:
            raise ValueError("interval does not isolate exactly one root")
        mult, factor = self._multiplicity_in_interval(iv)
        return RootCertificate(
            root_id=root_id or self._next_id(), kind="interval", point=None,
            interval=iv, multiplicity=mult, squarefree_factor=factor,
            variations_lo=vlo, variations_hi=vhi, signs_lo=slo, signs_hi=shi)

    def _pending(self, iv: Interval, cnt: int) -> PendingInterval:
        vlo, _ = self.variations(iv.lo)
        vhi, _ = self.variations(iv.hi)
        return PendingInterval(interval=iv, root_count=cnt,
                               variations_lo=vlo, variations_hi=vhi)

    # -- refinement -----------------------------------------------------
    def refine(self, iv: Interval, count: int, budget: Optional[List[int]]):
        """Bisect the region until every piece isolates <= 1 root.

        budget: mutable [remaining steps]; None means unlimited. Each
        bisection consumes one step. Returns (roots, pending).
        """
        roots: List[RootCertificate] = []
        pending: List[PendingInterval] = []
        queue: Deque[Tuple[Interval, int]] = deque([(iv, count)])
        while queue:
            cur, cnt = queue.popleft()
            if cnt == 0:
                continue
            if cnt == 1:
                roots.append(self.interval_certificate(cur))
                continue
            if budget is not None and budget[0] <= 0:
                pending.append(self._pending(cur, cnt))
                continue
            if budget is not None:
                budget[0] -= 1
            mid = (cur.lo + cur.hi) / 2
            vlo, _ = self.variations(cur.lo)
            vmid, _ = self.variations(mid)
            if evaluate(self.q, mid) == 0:
                # Exact rational root at the midpoint: extract as a point
                # root and split into (lo, mid) and (mid, hi] exactly.
                roots.append(self.point_certificate(mid))
                left_cnt = vlo - vmid - 1
                right_cnt = cnt - left_cnt - 1
                if left_cnt:
                    queue.append(
                        (Interval(cur.lo, mid, hi_excluded=True), left_cnt))
                if right_cnt:
                    queue.append(
                        (Interval(mid, cur.hi, cur.hi_excluded), right_cnt))
            else:
                left_cnt = vlo - vmid
                right_cnt = cnt - left_cnt
                if left_cnt:
                    queue.append((Interval(cur.lo, mid), left_cnt))
                if right_cnt:
                    queue.append(
                        (Interval(mid, cur.hi, cur.hi_excluded), right_cnt))
        return roots, pending


def _root_sort_key(cert: RootCertificate):
    if cert.kind == "point":
        return (cert.point, 0)
    return (cert.interval.lo, 1)


def isolate(p, max_steps: Optional[int] = None,
            id_prefix: str = "r") -> IsolationResult:
    """Isolate the distinct real roots of a rational polynomial.

    max_steps bounds the number of bisection steps; on exhaustion the
    result carries the already isolated roots plus pending intervals with
    their exact root counts. Multiplicities are listed separately via the
    square-free factorization. A polynomial with no real roots yields an
    empty root list.
    """
    eng = SturmEngine(p, id_prefix=id_prefix)
    if degree(eng.q) <= 0:
        return IsolationResult(eng.p, eng.q, eng.factors, [], [], True, 0)
    total = eng.total_roots()
    budget = None if max_steps is None else [max_steps]
    roots, pending = eng.refine(Interval(-eng.bound, eng.bound), total, budget)
    roots.sort(key=_root_sort_key)
    return IsolationResult(eng.p, eng.q, eng.factors, roots, pending,
                           completed=not pending, total_distinct_roots=total)
