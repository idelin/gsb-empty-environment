"""Independent verifier: re-checks every certificate from scratch.

The verifier does not trust the isolation pipeline.  Given only the input
polynomial and the reported result it independently recomputes:

  * the square-free factorization properties (square-freeness, exact
    multiplicities, pairwise coprimality, product equals f up to a unit);
  * every Sturm chain and every endpoint sign vector appearing in a
    certificate (evaluating the chain polynomials directly, re-deriving
    one-sided signs at exact endpoint roots);
  * every interval count, factor attribution, disjointness of the
    isolation, and (when the seeds cover all roots) the global root total;
  * every reused-interval certificate and matching edge of a revision.

All checks use exact rational arithmetic only.
"""
from __future__ import annotations

from fractions import Fraction

from .intervals import Interval
from .isolate import cauchy_bound
from .poly import (
    ONE,
    degree,
    derivative,
    divmod_poly,
    evaluate,
    gcd_poly,
    is_zero,
    lc,
    mul,
    normalize,
    power,
    primitive_integer,
)
from .squarefree import radical
from .sturm import sturm_chain


def _variations(signs) -> int:
    count = 0
    prev = 0
    for s in signs:
        if s == 0:
            continue
        if prev and s != prev:
            count += 1
        prev = s
    return count


def _signs_at(chain, bound):
    """Recompute an endpoint sign vector directly from the chain."""
    if bound is None:
        return tuple(
            (1 if lc(p) > 0 else -1) * (1 if degree(p) % 2 == 0 else -1)
            for p in chain
        ), ()
    if bound == "+inf":
        return tuple(1 if lc(p) > 0 else -1 for p in chain), ()
    x = Fraction(bound)
    values = [evaluate(p, x) for p in chain]
    if values[0] == 0:
        signs, dropped = [], []
        for i, v in enumerate(values):
            if v == 0:
                dropped.append(i)
            else:
                signs.append(1 if v > 0 else -1)
        return tuple(signs), tuple(dropped)
    return tuple(
        1 if v > 0 else (-1 if v < 0 else 0) for v in values
    ), ()


def _recount(chain, iv):
    lo_signs, lo_drop = _signs_at(chain, iv.lower)
    hi_signs, hi_drop = _signs_at(chain, iv.upper)
    lo_var = _variations(lo_signs)
    hi_var = _variations(hi_signs)
    # one-sided correction at exact endpoint roots (see sturm.py):
    # V(x-) = V(x+) + 1 for a square-free chain polynomial
    if lo_drop and iv.lower_closed:
        lo_var += 1
    if hi_drop and not iv.upper_closed:
        hi_var += 1
    return (
        lo_var - hi_var,
        lo_signs,
        hi_signs,
        lo_drop,
        hi_drop,
    )


def verify_squarefree_factorization(f, factors) -> bool:
    f = normalize(f)
    factors = list(factors)
    if degree(f) < 1:
        return len(factors) == 0
    product = ONE
    polys = []
    for fac in factors:
        fk = normalize(fac.poly)
        if fac.index != fac.multiplicity or fac.multiplicity < 1:
            return False
        if degree(fk) < 1 or lc(fk) != 1:
            return False
        if tuple(fac.primitive) != primitive_integer(fk):
            return False
        if degree(gcd_poly(fk, derivative(fk))) != 0:
            return False
        pk = power(fk, fac.multiplicity)
        if not is_zero(divmod_poly(f, pk)[1]):
            return False
        if is_zero(divmod_poly(f, mul(pk, fk))[1]):
            return False
        product = mul(product, pk)
        polys.append(fk)
    for i in range(len(polys)):
        for j in range(i + 1, len(polys)):
            if degree(gcd_poly(polys[i], polys[j])) != 0:
                return False
    quo, rem = divmod_poly(f, product)
    return is_zero(rem) and degree(quo) == 0


def verify_count_certificate(rad, cert) -> bool:
    rad = normalize(rad)
    if degree(rad) < 1:
        return False
    try:
        chain = sturm_chain(rad)
    except ValueError:
        return False
    if tuple(degree(p) for p in chain) != tuple(cert.chain_degrees):
        return False
    count, lo_signs, hi_signs, lo_drop, hi_drop = _recount(
        chain, cert.interval
    )
    if count != cert.count or not cert.check_arithmetic():
        return False
    iv = cert.interval
    exp_lo_side = (
        ("left" if iv.lower_closed else "right") if lo_drop else "exact"
    )
    exp_hi_side = (
        ("right" if iv.upper_closed else "left") if hi_drop else "exact"
    )
    if iv.lower is None or iv.lower == "-inf":
        exp_lo_side = "exact"
    if iv.upper is None or iv.upper == "+inf":
        exp_hi_side = "exact"
    lo, hi = cert.lower_endpoint, cert.upper_endpoint
    if tuple(lo.signs) != lo_signs or tuple(hi.signs) != hi_signs:
        return False
    if tuple(lo.zeros_dropped) != lo_drop:
        return False
    if tuple(hi.zeros_dropped) != hi_drop:
        return False
    if lo.side != exp_lo_side or hi.side != exp_hi_side:
        return False
    lo_var = _variations(lo_signs) + (1 if lo_drop and iv.lower_closed else 0)
    hi_var = _variations(hi_signs) + (1 if hi_drop and not iv.upper_closed else 0)
    if lo.variation != lo_var or hi.variation != hi_var:
        return False
    return True


def verify_root(result, root) -> bool:
    if not verify_count_certificate(result.radical, root.count_certificate):
        return False
    if root.count_certificate.count != 1:
        return False
    if root.count_certificate.interval != root.interval:
        return False
    expected = []
    for fac in result.squarefree_factors:
        chain = sturm_chain(fac.poly)
        count, _, _, _, _ = _recount(chain, root.interval)
        if count == 1:
            expected.append(fac.index)
    if sorted(f.index for f in root.factors) != sorted(expected):
        return False
    if len(root.factor_certificates) != len(root.factors):
        return False
    for fac, cert in zip(root.factors, root.factor_certificates):
        if not verify_count_certificate(fac.poly, cert):
            return False
        if cert.count != 1 or cert.interval != root.interval:
            return False
    if root.is_point:
        for fac in root.factors:
            if evaluate(fac.poly, root.interval.lower) != 0:
                return False
    return True


def verify_isolation(result) -> bool:
    if not verify_squarefree_factorization(
        result.polynomial, result.squarefree_factors
    ):
        return False
    if degree(normalize(result.polynomial)) < 1:
        return not result.isolated and not result.pending
    if normalize(result.radical) != normalize(
        radical(result.squarefree_factors)
    ):
        return False
    for root in result.isolated:
        if not verify_root(result, root):
            return False
    for pend in result.pending:
        if not verify_count_certificate(
            result.radical, pend.count_certificate
        ):
            return False
        if pend.count != pend.count_certificate.count or pend.count < 2:
            return False
    ivs = [r.interval for r in result.isolated] + [
        p.interval for p in result.pending
    ]
    for i in range(len(ivs)):
        for j in range(i + 1, len(ivs)):
            if ivs[i].intersect(ivs[j]) is not None:
                return False
    if result.seeds_cover_all_roots:
        bound = cauchy_bound(normalize(result.radical))
        chain = sturm_chain(normalize(result.radical))
        total, _, _, _, _ = _recount(
            chain, Interval(-bound, bound, False, False)
        )
        if total != result.total_certified_roots:
            return False
    if result.budget is None and result.pending:
        return False
    return True


def verify_revision(report) -> bool:
    from .revision import compute_edges

    if not verify_isolation(report.new_result):
        return False
    for reused in report.reused:
        if not verify_count_certificate(
            report.new_result.radical, reused.certificate
        ):
            return False
        if reused.new_count != reused.certificate.count:
            return False
        if reused.certificate.interval != reused.interval:
            return False
        ok = any(
            r.isolation_id in reused.old_root_ids
            and r.interval == reused.interval
            for r in report.old_result.isolated
        )
        if not ok:
            return False
    expected = {
        (e.old_id, e.new_id)
        for e in compute_edges(report.old_result, report.new_result)
    }
    reported = {(e.old_id, e.new_id) for e in report.edges}
    return expected == reported
