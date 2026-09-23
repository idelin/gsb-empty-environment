"""Budget-bounded Sturm isolation of distinct real roots.

Pipeline (every step states its domain of applicability):

  1. Square-free factorization f = c * prod f_k^k  (any nonconstant f in
     Q[x]; Yun's algorithm, characteristic 0).
  2. Radical R = prod f_k (monic, square-free): its real roots are exactly
     the DISTINCT real roots of f; multiplicities stay attached to the f_k
     (e.g. x^2 has one distinct root 0 with multiplicity 2).
  3. Sturm chain of R (domain: R square-free) plus the exact endpoint rule
     gives a certified count of distinct roots in any rational interval,
     including intervals whose endpoint is an exact root.
  4. Bisection isolation under a split budget.  The default seed is the
     Cauchy interval (-B, B), B = 1 + max|a_i| / |a_n|, which strictly
     contains every real root, so its endpoints are never roots.
  5. Each isolated interval is attributed to its square-free factor(s) f_k
     by an independent Sturm count of f_k over the interval (exact
     evaluation is a special case of the same certificate at a point).

Budget semantics: ``budget`` is the maximum number of bisection splits.
When it is exhausted the remaining unresolved intervals are returned as
``pending`` together with their certified root counts; isolation can be
resumed with ``resume_isolation``.
"""
from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from fractions import Fraction

from .certificates import RootCountCertificate
from .intervals import Interval
from .poly import ONE, Poly, degree, evaluate, lc, normalize
from .squarefree import radical, squarefree_factorization
from .sturm import endpoint_sign, sturm_chain


def cauchy_bound(p: Poly) -> Fraction:
    """B = 1 + max|a_i| / |a_n|; every real root x of p satisfies |x| < B."""
    lead = lc(p)
    m = max((abs(c) for c in p[:-1]), default=Fraction(0))
    return 1 + m / abs(lead)


def _endpoint(chain, bound, closed_side: str):
    """Sturm sign vector at an endpoint, choosing one-sided variation when
    the endpoint is an exact root of the chain polynomial."""
    if bound is None or bound == "+inf":
        return endpoint_sign(chain, bound)
    if evaluate(chain[0], Fraction(bound)) == 0:
        return endpoint_sign(chain, bound, closed_side)
    return endpoint_sign(chain, bound, "exact")


def count_with_cert(chain, iv: Interval):
    """Certified count of distinct roots of the (square-free) chain
    polynomial in iv; endpoint roots counted exactly via one-sided V."""
    # [a, b]: V(a-) - V(b+);  (a, b]: V(a+) - V(b+);
    # [a, b): V(a-) - V(b-);  (a, b): V(a+) - V(b-).
    lo = _endpoint(chain, iv.lower, "left" if iv.lower_closed else "right")
    hi = _endpoint(chain, iv.upper, "right" if iv.upper_closed else "left")
    count = lo.variation - hi.variation
    rule = (
        f"count = V(lower:{lo.side}) - V(upper:{hi.side}) = "
        f"{lo.variation} - {hi.variation} = {count}; Sturm theorem for the "
        f"square-free polynomial; when an endpoint is itself a root the "
        f"one-sided variation is used (zero chain entries dropped)"
    )
    cert = RootCountCertificate(
        interval=iv,
        chain_degrees=tuple(degree(p) for p in chain),
        lower_endpoint=lo,
        upper_endpoint=hi,
        count=count,
        rule=rule,
    )
    return count, cert


@dataclass
class RootIsolation:
    """One certified distinct real root of the input polynomial."""

    isolation_id: int
    interval: Interval
    factors: tuple
    count_certificate: RootCountCertificate
    factor_certificates: tuple

    @property
    def is_point(self) -> bool:
        return self.interval.is_point()

    @property
    def point(self):
        return self.interval.lower if self.is_point else None

    @property
    def multiplicity(self) -> int:
        return sum(f.multiplicity for f in self.factors)


@dataclass
class PendingInterval:
    """An interval whose roots could not be separated within the budget."""

    interval: Interval
    count: int
    count_certificate: RootCountCertificate
    source_tag: int | None = None


@dataclass
class IsolationResult:
    polynomial: Poly
    squarefree_factors: tuple
    radical: Poly
    isolated: list
    pending: list
    budget: int | None
    splits_used: int
    seeds_cover_all_roots: bool

    @property
    def complete(self) -> bool:
        return not self.pending

    @property
    def total_certified_roots(self) -> int:
        return len(self.isolated) + sum(p.count for p in self.pending)


class _Engine:
    def __init__(self, factors):
        self.factors = list(factors)
        self.rad = radical(self.factors)
        self.chain = sturm_chain(self.rad)
        self._factor_chains = {}

    def factor_chain(self, index: int):
        if index not in self._factor_chains:
            self._factor_chains[index] = sturm_chain(
                self.factors[index].poly
            )
        return self._factor_chains[index]

    def make_root(self, rid: int, iv: Interval, radical_cert=None):
        if radical_cert is None:
            _, radical_cert = count_with_cert(self.chain, iv)
        found = []
        fac_certs = []
        for i, fac in enumerate(self.factors):
            count, cert = count_with_cert(self.factor_chain(i), iv)
            if count == 1:
                found.append(fac)
                fac_certs.append(cert)
        return RootIsolation(
            rid, iv, tuple(found), radical_cert, tuple(fac_certs)
        )


def _sort_key(iv: Interval):
    return (1 if iv.lower is None else 0, iv.lower if iv.lower is not None
            else Fraction(0))


def isolate(
    f: Poly,
    budget=None,
    seeds=None,
    seeds_cover_all_roots=None,
) -> IsolationResult:
    """Isolate every distinct real root of f (all arithmetic in Q).

    budget: max number of bisection splits (None = unlimited).
    seeds: optional initial intervals (e.g. pending intervals from an
        interrupted run); None means the Cauchy cover (-B, B).
    seeds_cover_all_roots: whether the seeds together contain every real
        root (True automatically for the default Cauchy seed).

    Endpoint roots are never silently double counted: an exact root on a
    closed seed endpoint is peeled off as a point root {r} and the endpoint
    is opened before bisection continues.
    """
    f = normalize(f)
    factors = tuple(squarefree_factorization(f))
    if degree(f) < 1:
        return IsolationResult(f, (), ONE, [], [], budget, 0, True)

    eng = _Engine(factors)
    if seeds is None:
        bound = cauchy_bound(eng.rad)
        seeds = [Interval(-bound, bound, False, False)]
        cover = True
    else:
        cover = (
            False
            if seeds_cover_all_roots is None
            else seeds_cover_all_roots
        )

    queue = deque()
    isolated = []
    next_id = [1]
    splits = 0

    def make(iv, cert):
        isolated.append(eng.make_root(next_id[0], iv, cert))
        next_id[0] += 1

    def ingest(iv: Interval, tag):
        """Peel exact endpoint roots, then classify the remaining interval."""
        if iv.is_point():
            if evaluate(eng.rad, iv.lower) == 0:
                make(iv, None)
            return
        lo, hi = iv.lower, iv.upper
        lo_closed, hi_closed = iv.lower_closed, iv.upper_closed
        if lo is not None and lo_closed and evaluate(eng.rad, lo) == 0:
            make(Interval(lo, lo, True, True), None)
            lo_closed = False
        if hi is not None and hi_closed and evaluate(eng.rad, hi) == 0:
            make(Interval(hi, hi, True, True), None)
            hi_closed = False
        if lo is not None and hi is not None and lo == hi:
            return
        opened = Interval(lo, hi, lo_closed, hi_closed)
        count, cert = count_with_cert(eng.chain, opened)
        if count == 0:
            return
        if count == 1:
            make(opened, cert)
        else:
            queue.append(PendingInterval(opened, count, cert, tag))

    for tag, seed in enumerate(seeds):
        ingest(seed, tag)

    while queue:
        if budget is not None and splits >= budget:
            break
        item = queue.popleft()
        iv = item.interval
        if iv.lower is None or iv.upper is None:
            mid = Fraction(0)
        else:
            mid = (iv.lower + iv.upper) / 2
        splits += 1
        if evaluate(eng.rad, mid) == 0:
            # Exact midpoint root: it is emitted once as {mid}; the two
            # children open the midpoint so it cannot be counted twice.
            make(Interval(mid, mid, True, True), None)
            left = Interval(iv.lower, mid, iv.lower_closed, False)
            right = Interval(mid, iv.upper, False, iv.upper_closed)
        else:
            # mid is not a root: assign it to the right child only, so all
            # produced intervals are pairwise disjoint as sets (the count
            # is unaffected because the endpoint is not a root).
            left = Interval(iv.lower, mid, iv.lower_closed, False)
            right = Interval(mid, iv.upper, True, iv.upper_closed)
        ingest(left, item.source_tag)
        ingest(right, item.source_tag)

    pending = list(queue)
    isolated.sort(key=lambda r: _sort_key(r.interval))
    for rid, root in enumerate(isolated, start=1):
        root.isolation_id = rid
    return IsolationResult(
        f, factors, eng.rad, isolated, pending, budget, splits, cover
    )


def resume_isolation(result: IsolationResult, budget=None) -> IsolationResult:
    """Continue an interrupted isolation using its pending intervals."""
    if not result.pending:
        return result
    cont = isolate(
        result.polynomial,
        budget=budget,
        seeds=[p.interval for p in result.pending],
        seeds_cover_all_roots=result.seeds_cover_all_roots,
    )
    combined = list(result.isolated) + cont.isolated
    combined.sort(key=lambda r: _sort_key(r.interval))
    for rid, root in enumerate(combined, start=1):
        root.isolation_id = rid
    return IsolationResult(
        result.polynomial,
        result.squarefree_factors,
        result.radical,
        combined,
        cont.pending,
        budget,
        result.splits_used + cont.splits_used,
        result.seeds_cover_all_roots,
    )
